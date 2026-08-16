package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type ResourceKey struct {
	Path     string `json:"path"`
	Selector string `json:"selector"`
}

type Resource struct {
	Path     string `json:"path"`
	Selector string `json:"selector"`
	Digest   string `json:"digest"`
}

type Manifest struct {
	Resources []Resource `json:"resources"`
}

type OperationKind string

const (
	Create   OperationKind = "create"
	Update   OperationKind = "update"
	Remove   OperationKind = "remove"
	Conflict OperationKind = "conflict"
	Skip     OperationKind = "skip"
)

type Operation struct {
	Kind     OperationKind `json:"kind"`
	Path     string        `json:"path"`
	Selector string        `json:"selector"`
	Code     string        `json:"code,omitempty"`
	Reason   string        `json:"reason,omitempty"`
}

type ReconcilePlan struct {
	Operations []Operation `json:"operations"`
}

// ManifestFor builds a canonical resource-level ownership manifest for a staged snapshot.
func ManifestFor(snapshot Snapshot) (Manifest, error) {
	resources := make([]Resource, 0, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		contents, err := os.ReadFile(filepath.Join(snapshot.Stage, filepath.FromSlash(artifact.Path)))
		if err != nil {
			return Manifest{}, fmt.Errorf("read staged artifact %q: %w", artifact.Path, err)
		}
		if selectors := snapshot.ManagedSelectors[artifact.Path]; snapshot.decompose != nil && len(selectors) > 0 {
			decomposed, err := snapshot.decompose.Resources(artifact.Path, contents, selectors)
			if err != nil {
				return Manifest{}, err
			}
			resources = append(resources, decomposed...)
			continue
		}
		resources = append(resources, Resource{Path: artifact.Path, Selector: "file", Digest: resourceDigest(contents)})
	}
	sortResources(resources)
	return Manifest{Resources: resources}, nil
}

func resourceDigest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

// Plan compares desired managed resources with a prior manifest and live digests without mutating either.
func Plan(previous, desired Manifest, live map[ResourceKey]string) ReconcilePlan {
	prior := resourcesByKey(previous.Resources)
	next := resourcesByKey(desired.Resources)
	operations := make([]Operation, 0, len(prior)+len(next)+len(live))

	for _, resource := range desired.Resources {
		key := resourceKey(resource)
		old, wasManaged := prior[key]
		actual, exists := live[key]
		switch {
		case !wasManaged && !exists:
			operations = append(operations, operation(Create, resource, "", ""))
		case !wasManaged:
			operations = append(operations, operation(Conflict, resource, "render.ownership.conflict", "user-owned resource exists"))
		case actual != old.Digest:
			operations = append(operations, operation(Conflict, resource, "render.precondition.stale", "managed resource is stale"))
		case resource.Digest == old.Digest:
			operations = append(operations, operation(Skip, resource, "render.managed.unchanged", "managed resource is unchanged"))
		default:
			operations = append(operations, operation(Update, resource, "", ""))
		}
	}

	for key, resource := range prior {
		if _, stillDesired := next[key]; stillDesired {
			continue
		}
		if live[key] != resource.Digest {
			operations = append(operations, operation(Conflict, resource, "render.precondition.stale", "managed resource is stale"))
			continue
		}
		operations = append(operations, operation(Remove, resource, "", ""))
	}
	for key, digest := range live {
		if _, managed := prior[key]; managed {
			continue
		}
		if _, desired := next[key]; !desired {
			operations = append(operations, Operation{Kind: Skip, Path: key.Path, Selector: key.Selector, Code: "render.ownership.user", Reason: "user-owned resource is preserved"})
		}
		_ = digest
	}
	sort.Slice(operations, func(i, j int) bool {
		if operationRank(operations[i].Kind) != operationRank(operations[j].Kind) {
			return operationRank(operations[i].Kind) < operationRank(operations[j].Kind)
		}
		return operations[i].Path+operations[i].Selector < operations[j].Path+operations[j].Selector
	})
	return ReconcilePlan{Operations: operations}
}

func resourcesByKey(resources []Resource) map[ResourceKey]Resource {
	indexed := make(map[ResourceKey]Resource, len(resources))
	for _, resource := range resources {
		indexed[resourceKey(resource)] = resource
	}
	return indexed
}

func resourceKey(resource Resource) ResourceKey {
	return ResourceKey{Path: resource.Path, Selector: resource.Selector}
}

func operation(kind OperationKind, resource Resource, code, reason string) Operation {
	return Operation{Kind: kind, Path: resource.Path, Selector: resource.Selector, Code: code, Reason: reason}
}

func operationRank(kind OperationKind) int {
	return map[OperationKind]int{Create: 0, Update: 1, Remove: 2, Conflict: 3, Skip: 4}[kind]
}

func sortResources(resources []Resource) {
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].Path+resources[i].Selector < resources[j].Path+resources[j].Selector
	})
}
