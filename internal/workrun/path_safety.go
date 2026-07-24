package workrun

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

var (
	errUnsafeWorkRunPath   = errors.New("work run path is unavailable or unsafe")
	errWorkRunPathReplaced = errors.New("work run path identity changed")
)

// workRunDirectoryIdentity keeps the authority directory open while a
// mutation is in flight. Validate detects replacement of the path between
// lock acquisition and publication.
type workRunDirectoryIdentity struct {
	file *os.File
	info fs.FileInfo
	path string
}

func openWorkRunDirectoryIdentity(path string) (*workRunDirectoryIdentity, error) {
	before, err := privateWorkRunDirectoryInfo(path)
	if err != nil {
		return nil, err
	}
	file, err := openWorkRunPathNoFollow(path, true)
	if err != nil {
		return nil, fmt.Errorf("%w: open private directory: %v", errUnsafeWorkRunPath, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	after, err := privateWorkRunDirectoryInfo(path)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !opened.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) ||
		!privateOpenWorkRunPathSafe(file, opened) {
		_ = file.Close()
		return nil, errWorkRunPathReplaced
	}
	return &workRunDirectoryIdentity{file: file, info: opened, path: path}, nil
}

func (identity *workRunDirectoryIdentity) Validate() error {
	if identity == nil || identity.file == nil || identity.info == nil || identity.path == "" {
		return errUnsafeWorkRunPath
	}
	opened, err := identity.file.Stat()
	if err != nil {
		return err
	}
	current, err := privateWorkRunDirectoryInfo(identity.path)
	if err != nil {
		return err
	}
	if !opened.IsDir() || !os.SameFile(identity.info, opened) ||
		!os.SameFile(opened, current) ||
		!privateOpenWorkRunPathSafe(identity.file, opened) {
		return errWorkRunPathReplaced
	}
	return nil
}

func (identity *workRunDirectoryIdentity) Close() error {
	if identity == nil || identity.file == nil {
		return nil
	}
	err := identity.file.Close()
	identity.file = nil
	return err
}

// boundedWorkRunFile holds the exact private regular file opened after an
// Lstat/open/Lstat identity check.
type boundedWorkRunFile struct {
	file    *os.File
	info    fs.FileInfo
	path    string
	maximum int64
}

func openBoundedPrivateWorkRunFile(path string, maximum int64) (*boundedWorkRunFile, error) {
	if maximum < 1 {
		return nil, errors.New("work run file bound must be positive")
	}
	before, err := privateBoundedWorkRunFileInfo(path, maximum)
	if err != nil {
		return nil, err
	}
	file, err := openWorkRunPathNoFollow(path, false)
	if err != nil {
		return nil, fmt.Errorf("%w: open private file: %v", errUnsafeWorkRunPath, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	after, err := privateBoundedWorkRunFileInfo(path, maximum)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !opened.Mode().IsRegular() || opened.Size() < 1 || opened.Size() > maximum ||
		!os.SameFile(before, opened) || !os.SameFile(opened, after) ||
		!privateOpenWorkRunPathSafe(file, opened) {
		_ = file.Close()
		return nil, errWorkRunPathReplaced
	}
	return &boundedWorkRunFile{
		file: file, info: opened, path: path, maximum: maximum,
	}, nil
}

func (opened *boundedWorkRunFile) ReadAll() ([]byte, error) {
	if opened == nil || opened.file == nil || opened.info == nil || opened.maximum < 1 {
		return nil, errUnsafeWorkRunPath
	}
	if _, err := opened.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	payload, err := io.ReadAll(io.LimitReader(opened.file, opened.maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > opened.maximum {
		return nil, errors.New("work run authority artifact exceeds its byte bound")
	}
	if int64(len(payload)) != opened.info.Size() {
		return nil, errWorkRunPathReplaced
	}
	if err := opened.Validate(); err != nil {
		return nil, err
	}
	return payload, nil
}

func (opened *boundedWorkRunFile) Validate() error {
	if opened == nil || opened.file == nil || opened.info == nil || opened.path == "" {
		return errUnsafeWorkRunPath
	}
	currentOpen, err := opened.file.Stat()
	if err != nil {
		return err
	}
	currentPath, err := privateBoundedWorkRunFileInfo(opened.path, opened.maximum)
	if err != nil {
		return err
	}
	if !currentOpen.Mode().IsRegular() || currentOpen.Size() != opened.info.Size() ||
		!os.SameFile(opened.info, currentOpen) ||
		!os.SameFile(currentOpen, currentPath) ||
		!privateOpenWorkRunPathSafe(opened.file, currentOpen) {
		return errWorkRunPathReplaced
	}
	return nil
}

func (opened *boundedWorkRunFile) Close() error {
	if opened == nil || opened.file == nil {
		return nil
	}
	err := opened.file.Close()
	opened.file = nil
	return err
}

func readBoundedPrivateWorkRunFile(path string, maximum int64) ([]byte, error) {
	opened, err := openBoundedPrivateWorkRunFile(path, maximum)
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	return opened.ReadAll()
}

func validatePrivateWorkRunDirectory(path string) error {
	_, err := privateWorkRunDirectoryInfo(path)
	return err
}

func validatePrivateWorkRunFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return validatePrivateWorkRunInfo(path, info, false)
}

func privateWorkRunDirectoryInfo(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := validatePrivateWorkRunInfo(path, info, true); err != nil {
		return nil, err
	}
	return info, nil
}

func privateBoundedWorkRunFileInfo(path string, maximum int64) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := validatePrivateWorkRunInfo(path, info, false); err != nil {
		return nil, err
	}
	if info.Size() < 1 || info.Size() > maximum {
		return nil, errors.New("work run authority artifact is not a bounded regular file")
	}
	return info, nil
}

func validatePrivateWorkRunInfo(path string, info fs.FileInfo, directory bool) error {
	if path == "" || info == nil || workRunPathUnsafe(path, info) {
		return errUnsafeWorkRunPath
	}
	if directory {
		if !info.IsDir() {
			return errUnsafeWorkRunPath
		}
	} else if !info.Mode().IsRegular() {
		return errUnsafeWorkRunPath
	}
	if !privateWorkRunPathSafe(path, info) {
		return errors.New("work run path permissions are not owner-only")
	}
	return nil
}
