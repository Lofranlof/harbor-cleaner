package k8s

import (
	"context"
	"sync"
	"time"

	"harbor-cleaner/internal/ports"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// WorkloadSource implements ports.WorkloadSource by reading the pod template
// of every Deployment, StatefulSet, DaemonSet, Job and CronJob across every
// namespace of each configured cluster - not by listing live Pods.
//
// Reading controller templates instead of live pods means scale-to-zero
// Deployments, CronJobs between scheduled runs, and Jobs whose Pods have
// already been garbage-collected (ttlSecondsAfterFinished) all still count as
// "in use". The known gap is bare Pods created without an owning controller
// (kubectl run/debug, or workflow engines like Argo Workflows/Tekton/Spark-on-k8s
// that schedule Pods directly) - those are not covered in this version.
type WorkloadSource struct {
	clientsets []kubernetes.Interface
	timeout    time.Duration
}

var _ ports.WorkloadSource = (*WorkloadSource)(nil)

func NewWorkloadSource(clientsets []kubernetes.Interface, timeout time.Duration) *WorkloadSource {
	return &WorkloadSource{clientsets: clientsets, timeout: timeout}
}

func (w *WorkloadSource) LiveImageRefs(ctx context.Context) (map[string]struct{}, error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	type result struct {
		images map[string]struct{}
		err    error
	}
	resultsCh := make(chan result, len(w.clientsets))
	var wg sync.WaitGroup

	for _, clientset := range w.clientsets {
		clientset := clientset
		wg.Add(1)
		go func() {
			defer wg.Done()
			images, err := imagesFromCluster(ctxWithTimeout, clientset)
			resultsCh <- result{images: images, err: err}
		}()
	}
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	merged := make(map[string]struct{})
	for res := range resultsCh {
		if res.err != nil {
			return nil, res.err
		}
		for image := range res.images {
			merged[image] = struct{}{}
		}
	}
	return merged, nil
}

// imagesFromCluster collects every container/init-container image referenced
// by a workload controller's pod template in one cluster.
func imagesFromCluster(ctx context.Context, clientset kubernetes.Interface) (map[string]struct{}, error) {
	images := make(map[string]struct{})
	opts := metav1.ListOptions{}

	deployments, err := clientset.AppsV1().Deployments("").List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, d := range deployments.Items {
		collectPodSpecImages(images, d.Spec.Template.Spec)
	}

	statefulSets, err := clientset.AppsV1().StatefulSets("").List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, s := range statefulSets.Items {
		collectPodSpecImages(images, s.Spec.Template.Spec)
	}

	daemonSets, err := clientset.AppsV1().DaemonSets("").List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, d := range daemonSets.Items {
		collectPodSpecImages(images, d.Spec.Template.Spec)
	}

	jobs, err := clientset.BatchV1().Jobs("").List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs.Items {
		collectPodSpecImages(images, j.Spec.Template.Spec)
	}

	cronJobs, err := clientset.BatchV1().CronJobs("").List(ctx, opts)
	if err != nil {
		return nil, err
	}
	for _, cj := range cronJobs.Items {
		collectPodSpecImages(images, cj.Spec.JobTemplate.Spec.Template.Spec)
	}

	return images, nil
}

func collectPodSpecImages(images map[string]struct{}, spec corev1.PodSpec) {
	for _, c := range spec.Containers {
		images[c.Image] = struct{}{}
	}
	for _, c := range spec.InitContainers {
		images[c.Image] = struct{}{}
	}
}
