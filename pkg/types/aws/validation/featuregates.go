package validation

import (
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"

	features "github.com/openshift/api/features"
	"github.com/openshift/installer/pkg/types"
	"github.com/openshift/installer/pkg/types/dns"
	"github.com/openshift/installer/pkg/types/featuregates"
)

// GatedFeatures determines all of the install config fields that should
// be validated to ensure that the proper featuregate is enabled when the field is used.
func GatedFeatures(c *types.InstallConfig) []featuregates.GatedInstallConfigFeature {
	gatedFeatures := []featuregates.GatedInstallConfigFeature{
		{
			FeatureGateName: features.FeatureGateAWSClusterHostedDNSInstall,
			Condition:       c.AWS.UserProvisionedDNS == dns.UserProvisionedDNSEnabled,
			Field:           field.NewPath("platform", "aws", "userProvisionedDNS"),
		},
		{
			FeatureGateName: features.FeatureGateAWSDualStackInstall,
			Condition:       c.AWS.IPFamily.DualStackEnabled(),
			Field:           field.NewPath("platform", "aws", "ipFamily"),
		},
		{
			FeatureGateName: features.FeatureGateAWSEuropeanSovereignCloudInstall,
			Condition:       strings.HasPrefix(c.AWS.Region, "eusc-"),
			Field:           field.NewPath("platform", "aws", "region"),
		},
	}

	// Check if any compute pool has dedicated hosts configured
	for idx, compute := range c.Compute {
		if compute.Platform.AWS != nil && compute.Platform.AWS.HostPlacement != nil {
			gatedFeatures = append(gatedFeatures, featuregates.GatedInstallConfigFeature{
				FeatureGateName: features.FeatureGateAWSDedicatedHosts,
				Condition:       true,
				Field:           field.NewPath("compute").Index(idx).Child("platform", "aws", "hostPlacement"),
			})
			break // Only need to add the feature gate once
		}
	}

	// Check for non-root volumes (multi-disk) on default, controlPlane, and compute pools
	if c.AWS.DefaultMachinePlatform != nil && len(c.AWS.DefaultMachinePlatform.NonRootVolumes) != 0 {
		gatedFeatures = append(gatedFeatures, featuregates.GatedInstallConfigFeature{
			FeatureGateName: features.FeatureGateAWSMultiDisk,
			Condition:       true,
			Field:           field.NewPath("platform", "aws", "defaultMachinePlatform", "nonRootVolumes"),
		})
	}

	if c.ControlPlane != nil && c.ControlPlane.Platform.AWS != nil && len(c.ControlPlane.Platform.AWS.NonRootVolumes) != 0 {
		gatedFeatures = append(gatedFeatures, featuregates.GatedInstallConfigFeature{
			FeatureGateName: features.FeatureGateAWSMultiDisk,
			Condition:       true,
			Field:           field.NewPath("controlPlane", "platform", "aws", "nonRootVolumes"),
		})
	}

	for idx, compute := range c.Compute {
		if compute.Platform.AWS != nil && len(compute.Platform.AWS.NonRootVolumes) != 0 {
			gatedFeatures = append(gatedFeatures, featuregates.GatedInstallConfigFeature{
				FeatureGateName: features.FeatureGateAWSMultiDisk,
				Condition:       true,
				Field:           field.NewPath("compute").Index(idx).Child("platform", "aws", "nonRootVolumes"),
			})
			break
		}
	}

	return gatedFeatures
}
