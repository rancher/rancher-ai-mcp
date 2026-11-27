package utils

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestGetPodLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/namespaces/cattle-system/pods/reader/log", r.URL.Path)
		assert.Equal(t, "30", r.URL.Query().Get("tailLines"))
		if r.URL.Query().Get("container") == "failed" {
			http.Error(w, "log access denied", http.StatusForbidden)
			return
		}
		fmt.Fprint(w, "system-agent logs")
	}))
	defer server.Close()

	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: "cattle-system"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "healthy"}, {Name: "failed"}}},
	}

	logs, err := GetPodLogs(t.Context(), clientset, pod, 30)
	require.Error(t, err)
	assert.ErrorContains(t, err, "container failed")
	assert.ErrorContains(t, err, "log access denied")
	assert.Equal(t, map[string]any{"healthy": "system-agent logs"}, logs.Logs)

	pod.Spec.Containers = pod.Spec.Containers[:1]
	logs, err = GetPodLogs(t.Context(), clientset, pod, 30)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"healthy": "system-agent logs"}, logs.Logs)
}
