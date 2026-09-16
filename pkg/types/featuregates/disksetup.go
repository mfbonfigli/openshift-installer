package featuregates

import (
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/installer/pkg/types/azure"
)

// DiskSetupFeatureGate returns the name of the feature gate that guards the diskSetup
// machine pool field for the given platform.
//
// Disk setup on Azure is part of the Azure multi-disk feature and is therefore gated by
// AzureMultiDisk, so that Azure multi-disk can be released independently of the
// platform-agnostic MultiDiskSetup gate that guards every other platform.
func DiskSetupFeatureGate(platformName string) configv1.FeatureGateName {
	if platformName == azure.Name {
		return features.FeatureGateAzureMultiDisk
	}
	return features.FeatureGateMultiDiskSetup
}
