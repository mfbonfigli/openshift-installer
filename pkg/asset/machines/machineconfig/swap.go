package machineconfig

import (
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
)

// ForSwapEnabled generates a KubeletConfig that sets failSwapOn=false for the
// given role. This is required when a swap disk is configured via diskSetup —
// the swap partition activates before kubelet starts, and kubelet's default
// behavior is to refuse to start if swap is active.
func ForSwapEnabled(role string) (*mcfgv1.KubeletConfig, error) {
	kubeletConfigJSON, err := json.Marshal(map[string]any{
		"failSwapOn": false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal kubelet config: %w", err)
	}

	return &mcfgv1.KubeletConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: mcfgv1.SchemeGroupVersion.String(),
			Kind:       "KubeletConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("99-swap-%s", role),
		},
		Spec: mcfgv1.KubeletConfigSpec{
			MachineConfigPoolSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"machineconfiguration.openshift.io/mco-built-in": "",
					"pools.operator.machineconfiguration.openshift.io/" + role: "",
				},
			},
			KubeletConfig: &runtime.RawExtension{
				Raw: kubeletConfigJSON,
			},
		},
	}, nil
}
