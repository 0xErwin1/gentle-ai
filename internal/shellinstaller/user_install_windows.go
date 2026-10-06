//go:build windows

package shellinstaller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"

	assets "github.com/gentleman-programming/gentle-ai/v4/scripts"
	"golang.org/x/sys/windows"
)

const userWindowsSchema = "gentle-shell-windows-separate/v1"

type userWindowsManifest struct {
	Schema, Destination, SID, Identity, SupervisorSHA string
	PrefixIdentity, AgentIdentity                     string
}

func UserKernelCheck() error {
	version := windows.RtlGetVersion()
	var processMachine, nativeMachine uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &processMachine, &nativeMachine); err != nil {
		return err
	}
	if runtime.GOARCH != "amd64" || nativeMachine != 0x8664 || version.MajorVersion != 10 || version.BuildNumber < 22000 || version.ProductType != 1 {
		return errors.New("Gentle Shell Separate requires Windows 11 x64; Windows Server and ARM64 are not qualified targets")
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("run Gentle Shell from your normal Windows account, not an Administrator terminal")
	}
	return nil
}

func userWindowsWorkerCheck() error {
	if err := UserKernelCheck(); err != nil {
		return err
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		return err
	}
	var cpu userWindowsCPU
	if err := windows.QueryInformationJobObject(0, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu)), nil); err != nil {
		return err
	}
	if limits.BasicLimitInformation.LimitFlags != userWindowsJobFlags || limits.BasicLimitInformation.ActiveProcessLimit != 64 || limits.JobMemoryLimit != 3221225472 || cpu != (userWindowsCPU{Flags: 5, Rate: uint32(10000 / runtime.NumCPU())}) {
		return errors.New("physical Windows worker Job Object limits differ")
	}
	return nil
}

func InspectUserInstall(req UserInstallRequest) (string, error) {
	if err := UserKernelCheck(); err != nil {
		return "", err
	}
	if req.Mode != "separate" || req.SharedPrefix != "" || req.SharedAgent != "" || filepath.Base(req.Destination) == "." || filepath.Base(req.Destination) == string(filepath.Separator) {
		return "", errors.New("Windows MVP supports Separate only; existing Pi, configuration and bindings stay untouched")
	}
	parent, err := userWindowsIdentity(filepath.Dir(req.Destination), true)
	if err != nil || !filepath.IsAbs(req.Destination) || filepath.Clean(req.Destination) != req.Destination || strings.ContainsAny(req.Destination, "\x00\r\n\"%&|<>^!") {
		return "", errors.Join(err, errors.New("select an owned canonical Windows installation path"))
	}
	identity := "absent"
	if _, err := os.Lstat(req.Destination); err == nil {
		selected, err := userWindowsIdentity(req.Destination, true)
		if err != nil {
			return "", err
		}
		manifest, err := userWindowsManifestRead(req.Destination)
		if err != nil || manifest.Identity != selected {
			return "", errors.Join(err, errors.New("destination is not a completed owned Windows installation; no overwrite or recovery attempted"))
		}
		identity = selected
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	sid, err := userWindowsSID()
	if err != nil {
		return "", err
	}
	return userConfirmation(req, sid+"|"+parent+"|"+identity), nil
}

func userWindowsEnvironment(root string) ([]string, error) {
	system, err := windows.GetWindowsDirectory()
	if err != nil {
		return nil, err
	}
	if _, err := userWindowsIdentity(system, false); err != nil {
		return nil, err
	}
	env := []string{"SystemRoot=" + system, "WINDIR=" + system, "SystemDrive=" + filepath.VolumeName(system)}
	if root == "" {
		return append(env, "PATH="+filepath.Join(system, "System32")), nil
	}
	prefix, agent := filepath.Join(root, "prefix"), filepath.Join(root, "agent")
	return append(env,
		"HOME="+filepath.Join(root, "home"), "USERPROFILE="+filepath.Join(root, "home"),
		"APPDATA="+filepath.Join(root, "config"), "LOCALAPPDATA="+filepath.Join(root, "state"),
		"TEMP="+filepath.Join(root, "tmp"), "TMP="+filepath.Join(root, "tmp"),
		"GENTLE_PI_CONFIG_HOME="+filepath.Join(root, "config"), "PI_CODING_AGENT_DIR="+agent,
		"GENTLE_PI_AGENT_HOME="+agent, "GENTLE_PI_NO_SKILL_REGISTRY=1",
		"PATH="+strings.Join([]string{filepath.Join(root, "runtime/node"), filepath.Join(root, "runtime/go/bin"), filepath.Join(system, "System32")}, string(os.PathListSeparator)),
		"NPM_CONFIG_PREFIX="+prefix, "NPM_CONFIG_IGNORE_SCRIPTS=true", "NPM_CONFIG_AUDIT=false", "NPM_CONFIG_FUND=false",
		"NPM_CONFIG_USERCONFIG="+filepath.Join(root, "config/user.npmrc"), "NPM_CONFIG_GLOBALCONFIG="+filepath.Join(root, "config/global.npmrc"),
		"NPM_CONFIG_CACHE="+filepath.Join(root, "runtime/cache"), "NODE_USE_SYSTEM_CA=1"), nil
}

func userWindowsBinding(root, product string) []byte {
	return []byte("@echo off\r\n\"" + filepath.Join(root, "supervisor.exe") + "\" shell launch \"" + root + "\" " + product + " %*\r\n")
}

func userWindowsManifestRead(root string) (userWindowsManifest, error) {
	var manifest userWindowsManifest
	data, err := userWindowsRead(filepath.Join(root, "manifest.json"), 4096)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	canonical, _ := json.Marshal(manifest)
	identity, identityErr := userWindowsIdentity(root, true)
	sid, sidErr := userWindowsSID()
	if string(data) != string(canonical)+"\n" || manifest.Schema != userWindowsSchema || manifest.Destination != root || manifest.SID != sid || manifest.Identity != identity || identityErr != nil || sidErr != nil {
		return manifest, errors.Join(identityErr, sidErr, errors.New("Windows manifest/physical owner differs"))
	}
	return manifest, nil
}

// Keep whole small failures, never a plausible-looking prefix of a large one.
// The sanitized worker environment contains no caller credentials or config.
type userWindowsProvisionDiagnostic struct {
	data     []byte
	withheld bool
}

func (d *userWindowsProvisionDiagnostic) Write(p []byte) (int, error) {
	if d.withheld || len(p) > (16<<10)-len(d.data) {
		d.data, d.withheld = nil, true
	} else {
		d.data = append(d.data, p...)
	}
	return len(p), nil // Always drain the pipe, including wholly withheld output.
}

func (d *userWindowsProvisionDiagnostic) String() string {
	if d.withheld || !utf8.Valid(d.data) || strings.ContainsRune(string(d.data), 0) {
		return "whole diagnostic withheld (size, UTF-8, or NUL guard)"
	}
	return fmt.Sprintf("%q", d.data) // Escape terminal controls rather than emitting them.
}

func userWindowsProvision(ctx context.Context, root, action string, stdout, stderr io.Writer) error {
	helper, err := assets.ReadWindowsUserHelper()
	if err != nil {
		return err
	}
	actual, err := userWindowsRead(filepath.Join(root, "provision.mjs"), int64(len(helper)))
	if err != nil || string(actual) != string(helper) {
		return errors.Join(err, errors.New("Windows provision source differs"))
	}
	env, err := userWindowsEnvironment(root)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, filepath.Join(root, "runtime/node/node.exe"), filepath.Join(root, "provision.mjs"), root, action)
	command.Dir, command.Env = filepath.Join(root, "project"), env
	diagnostic := &userWindowsProvisionDiagnostic{}
	command.Stdout, command.Stderr, command.WaitDelay = stdout, io.MultiWriter(stderr, diagnostic), 2*time.Second
	if err := command.Run(); err != nil { // Already inside the read-back bounded Job Object.
		return fmt.Errorf("Windows provision %s failed: %w; stderr=%s", action, err, diagnostic)
	}
	return nil
}

func userWindowsInventory(root string) error {
	var count int
	var total int64
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, cause error) error {
		if cause != nil {
			return cause
		}
		count++
		if count > 250000 {
			return errors.New("Windows inventory file-count bound")
		}
		if _, err := userWindowsIdentity(path, true); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return errors.New("Windows inventory contains nonregular objects")
			}
			total += info.Size()
			bound := int64(32 << 20)
			if strings.HasPrefix(path, filepath.Join(root, "runtime/node")+string(filepath.Separator)) || strings.HasPrefix(path, filepath.Join(root, "runtime/go")+string(filepath.Separator)) || path == filepath.Join(root, "runtime/archives/go.zip") || path == filepath.Join(root, "runtime/archives/node.zip") || path == filepath.Join(root, "supervisor.exe") {
				bound = 256 << 20
			}
			if info.Size() > bound || total > 2<<30 {
				return errors.New("Windows inventory physical byte bound")
			}
		}
		return nil
	})
}

func RunUserInstall(ctx context.Context, req UserInstallRequest) (result UserInstallResult, err error) {
	if ctx == nil || ctx.Err() != nil {
		return result, errors.New("Windows installation canceled")
	}
	if err := userWindowsWorkerCheck(); err != nil {
		return result, err
	}
	confirmation, err := InspectUserInstall(req)
	if err != nil || confirmation != req.Confirmation {
		return result, errors.Join(err, errors.New("physical Windows selection confirmation differs"))
	}
	if _, err := os.Lstat(req.Destination); err == nil {
		if err := userWindowsVerify(ctx, req.Destination, io.Discard, io.Discard); err != nil {
			return result, err
		}
		return UserInstallResult{req.Destination, filepath.Join(req.Destination, "prefix"), filepath.Join(req.Destination, "agent"), "already-installed"}, nil
	}
	parent := filepath.Dir(req.Destination)
	stage, err := os.MkdirTemp(parent, ".gentle-shell-windows-stage-")
	if err != nil {
		return result, err
	}
	stageIdentity, identityErr := userWindowsIdentity(stage, true)
	if identityErr != nil {
		return result, identityErr // Do not claim ownership or remove an unknown stage.
	}
	published := false
	defer func() {
		if published {
			return
		}
		observed, cause := userWindowsIdentity(stage, true)
		if cause != nil || observed != stageIdentity {
			err = errors.Join(err, cause, fmt.Errorf("uncertain Windows stage identity; preserved %q", stage))
			return
		}
		if cause := userWindowsInventory(stage); cause != nil {
			err = errors.Join(err, cause, fmt.Errorf("uncertain Windows stage inventory; preserved %q", stage))
			return
		}
		err = errors.Join(err, os.RemoveAll(stage))
	}()
	if err := userWindowsPrivate(stage); err != nil {
		return result, err
	}
	for _, directory := range []string{"home", "config", "state", "agent/bin", "tmp", "project", "runtime/cache", "runtime/archives", "runtime/node", "runtime/go", "bin"} {
		if err := os.MkdirAll(filepath.Join(stage, filepath.FromSlash(directory)), 0700); err != nil {
			return result, err
		}
	}
	selection, _ := json.Marshal(req)
	if err := userWindowsWrite(filepath.Join(stage, "selection.json"), selection); err != nil {
		return result, err
	}
	started := time.Now()
	phase := func(step, artifact string) {
		fmt.Fprintf(os.Stderr, "Windows install phase: %s %s %dms\n", step, artifact, time.Since(started).Milliseconds())
	}
	for i, artifact := range userWindowsArtifacts {
		phase("acquire", artifact.Archive)
		data, err := userWindowsAcquire(ctx, artifact, filepath.Join(stage, "runtime/archives", artifact.Archive))
		if err != nil {
			return result, err
		}
		phase("unpack", artifact.Archive)
		if i < 2 {
			directory := "node"
			if i == 1 {
				directory = "go"
			}
			if _, err := userWindowsZIP(ctx, data, artifact, filepath.Join(stage, "runtime", directory), ""); err != nil {
				return result, err
			}
		} else {
			name := strings.TrimSuffix(artifact.Archive, ".zip") + ".exe"
			member, err := userWindowsZIP(ctx, data, artifact, "", artifact.Prefix+"/"+name)
			if err != nil {
				return result, err
			}
			if err := userWindowsWrite(filepath.Join(stage, "agent/bin", name), member); err != nil {
				return result, err
			}
		}
	}
	phase("runtimes-ready", "all")
	for _, name := range []string{"user.npmrc", "global.npmrc"} {
		if err := userWindowsWrite(filepath.Join(stage, "config", name), nil); err != nil {
			return result, err
		}
	}
	helper, err := assets.ReadWindowsUserHelper()
	if err != nil {
		return result, err
	}
	if err := userWindowsWrite(filepath.Join(stage, "provision.mjs"), helper); err != nil {
		return result, err
	}
	lockHelper, err := assets.ReadPrivateHelper("complete-generated-lock-sri.mjs")
	if err != nil {
		return result, err
	}
	if err := userWindowsWrite(filepath.Join(stage, "complete-generated-lock-sri.mjs"), lockHelper); err != nil {
		return result, err
	}
	self, err := os.Executable()
	if err != nil {
		return result, err
	}
	image, err := userWindowsReadTrusted(self, 256<<20, false)
	if err != nil {
		return result, err
	}
	if err := userWindowsWrite(filepath.Join(stage, "supervisor.exe"), image); err != nil {
		return result, err
	}
	phase("provision", "stock")
	if err := userWindowsProvision(ctx, stage, "install", io.Discard, io.Discard); err != nil {
		return result, err
	}
	phase("provision-ready", "stock")
	for _, product := range []string{"pi", "gentle-shell"} {
		if err := userWindowsWrite(filepath.Join(stage, "bin", product+".cmd"), userWindowsBinding(req.Destination, product)); err != nil {
			return result, err
		}
	}
	if err := userWindowsInventory(stage); err != nil {
		return result, err
	}
	observed, err := InspectUserInstall(req)
	if err != nil || observed != req.Confirmation {
		return result, errors.Join(err, errors.New("Windows selection changed before publication"))
	}
	prefixID, err := userWindowsIdentity(filepath.Join(stage, "prefix"), true)
	if err != nil {
		return result, err
	}
	agentID, err := userWindowsIdentity(filepath.Join(stage, "agent"), true)
	if err != nil {
		return result, err
	}
	sid, err := userWindowsSID()
	if err != nil {
		return result, err
	}
	manifest := userWindowsManifest{userWindowsSchema, req.Destination, sid, stageIdentity, userWindowsSHA(image), prefixID, agentID}
	encoded, _ := json.Marshal(manifest)
	if err := userWindowsWrite(filepath.Join(stage, "manifest.json"), append(encoded, '\n')); err != nil {
		return result, err
	}
	from, _ := windows.UTF16PtrFromString(stage)
	to, _ := windows.UTF16PtrFromString(req.Destination)
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return result, err
	} // No replace-existing flag.
	published = true
	if err := userWindowsVerify(ctx, req.Destination, io.Discard, io.Discard); err != nil {
		return result, errors.Join(err, errors.New("published Windows installation remains unqualified; preserved, no success reported"))
	}
	return UserInstallResult{req.Destination, filepath.Join(req.Destination, "prefix"), filepath.Join(req.Destination, "agent"), "installed"}, nil
}
