package ports

import "context"

// WorkloadSource reports which images are currently considered "in use" by
// some workload, so the cleaner can preserve them regardless of age. It
// replaces the old string-switch workload-source config (k8s/vers/k8sAndVers) -
// swapping the concrete adapter is now enough to change where "in use" comes
// from (see internal/adapters/k8s and internal/adapters/noworkload).
type WorkloadSource interface {
	// LiveImageRefs returns the set of image references (as they appear in a
	// container spec, e.g. "registry.example.com/myproject/myrepo:v1.2.3") that
	// are currently considered in use. The caller is responsible for mapping
	// these refs to artifact digests - a WorkloadSource has no notion of Harbor
	// digests, only of what image ref a workload was configured with.
	LiveImageRefs(ctx context.Context) (map[string]struct{}, error)
}
