package override

import (
	"testing"
)

func Test_getOperatorNamespace(t *testing.T) {
	tests := map[string]string{
		"kube-apiserver":      "openshift-kube-apiserver-operator",
		"kube-scheduler":      "openshift-kube-scheduler-operator",
		"openshift-apiserver": "openshift-apiserver-operator",
		"insights":            "openshift-insights",
	}

	for name, expected := range tests {
		t.Run(name, func(t *testing.T) {
			if got := getOperatorNamespace(name); got != expected {
				t.Errorf("expected operator namespace %q, got %q", expected, got)
			}
		})
	}
}
