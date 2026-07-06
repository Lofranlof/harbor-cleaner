// Package noworkload provides a trivial ports.WorkloadSource for running the
// cleaner without any Kubernetes access at all - retention then relies solely
// on the age/top-N/allow-list rules.
package noworkload

import (
	"context"

	"harbor-cleaner/internal/ports"
)

type WorkloadSource struct{}

var _ ports.WorkloadSource = WorkloadSource{}

func New() WorkloadSource { return WorkloadSource{} }

func (WorkloadSource) LiveImageRefs(ctx context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}
