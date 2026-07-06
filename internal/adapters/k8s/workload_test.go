package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func podSpec(image string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: image}},
		},
	}
}

// TestLiveImageRefsCoversWorkloadControllersNotBarePods verifies the fix for the
// original bug: a scale-to-zero Deployment and a CronJob that hasn't fired yet
// still contribute their image, even though neither has a live Pod right now.
func TestLiveImageRefsCoversWorkloadControllersNotBarePods(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "scaled-to-zero", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: int32Ptr(0), // scaled to zero: no live Pods exist
				Template: podSpec("registry.example.com/proj/repo:from-deployment"),
			},
		},
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "sts", Namespace: "default"},
			Spec:       appsv1.StatefulSetSpec{Template: podSpec("registry.example.com/proj/repo:from-statefulset")},
		},
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "ds", Namespace: "default"},
			Spec:       appsv1.DaemonSetSpec{Template: podSpec("registry.example.com/proj/repo:from-daemonset")},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "one-off-job", Namespace: "default"},
			Spec:       batchv1.JobSpec{Template: podSpec("registry.example.com/proj/repo:from-job")},
		},
		&batchv1.CronJob{
			// a CronJob between scheduled runs has no Job/Pod at all
			ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default"},
			Spec: batchv1.CronJobSpec{
				JobTemplate: batchv1.JobTemplateSpec{
					Spec: batchv1.JobSpec{Template: podSpec("registry.example.com/proj/repo:from-cronjob")},
				},
			},
		},
	)

	source := NewWorkloadSource([]kubernetes.Interface{clientset}, time.Minute)
	refs, err := source.LiveImageRefs(context.Background())
	require.NoError(t, err)

	assert.Equal(t, map[string]struct{}{
		"registry.example.com/proj/repo:from-deployment":  {},
		"registry.example.com/proj/repo:from-statefulset": {},
		"registry.example.com/proj/repo:from-daemonset":   {},
		"registry.example.com/proj/repo:from-job":         {},
		"registry.example.com/proj/repo:from-cronjob":     {},
	}, refs)
}

func int32Ptr(i int32) *int32 { return &i }
