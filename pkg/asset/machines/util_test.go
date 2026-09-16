package machines

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"
	capz "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/installer/pkg/asset/installconfig"
	"github.com/openshift/installer/pkg/asset/machines/vsphere"
	"github.com/openshift/installer/pkg/types"
	azuretypes "github.com/openshift/installer/pkg/types/azure"
	gcptypes "github.com/openshift/installer/pkg/types/gcp"
	vspheretypes "github.com/openshift/installer/pkg/types/vsphere"
)

func diskSetupInstallConfig(platform types.Platform, gates ...string) *installconfig.InstallConfig {
	return installconfig.MakeAsset(&types.InstallConfig{
		Platform:     platform,
		FeatureSet:   configv1.CustomNoUpgrade,
		FeatureGates: gates,
	})
}

func TestResolveDataDisk(t *testing.T) {
	etcdSetup := types.Disk{
		Type: types.Etcd,
		Etcd: &types.DiskEtcd{PlatformDiskID: "etcd"},
	}
	userDefinedSetup := types.Disk{
		Type:        types.UserDefined,
		UserDefined: &types.DiskUserDefined{PlatformDiskID: "userdisk", MountPath: "/mnt/data"},
	}

	azurePlatform := types.Platform{Azure: &azuretypes.Platform{Region: "centralus"}}
	vspherePlatform := types.Platform{VSphere: &vspheretypes.Platform{}}

	azurePool := func() *types.MachinePool {
		return &types.MachinePool{
			Name:      "master",
			DiskSetup: []types.Disk{etcdSetup, userDefinedSetup},
			Platform: types.MachinePoolPlatform{
				Azure: &azuretypes.MachinePool{
					DataDisks: []capz.DataDisk{
						{NameSuffix: "etcd", DiskSizeGB: 100, Lun: ptr.To(int32(0))},
						{NameSuffix: "userdisk", DiskSizeGB: 100, Lun: ptr.To(int32(1))},
					},
				},
			},
		}
	}

	vspherePool := func() *types.MachinePool {
		return &types.MachinePool{
			Name:      "master",
			DiskSetup: []types.Disk{etcdSetup, userDefinedSetup},
			Platform: types.MachinePoolPlatform{
				VSphere: &vspheretypes.MachinePool{
					DataDisks: []vspheretypes.DataDisk{
						{Name: "userdisk", SizeGiB: 100},
						{Name: "etcd", SizeGiB: 100},
					},
				},
			},
		}
	}

	cases := []struct {
		name          string
		installConfig *installconfig.InstallConfig
		pool          *types.MachinePool
		diskSetup     types.Disk
		index         int
		expected      any
		expectedError string
	}{
		{
			name:          "azure resolves the data disk at the same index",
			installConfig: diskSetupInstallConfig(azurePlatform, "AzureMultiDisk=true", "MultiDiskSetup=false"),
			pool:          azurePool(),
			diskSetup:     userDefinedSetup,
			index:         1,
			expected:      capz.DataDisk{NameSuffix: "userdisk", DiskSizeGB: 100, Lun: ptr.To(int32(1))},
		},
		{
			name:          "azure does not require the MultiDiskSetup feature gate",
			installConfig: diskSetupInstallConfig(azurePlatform, "AzureMultiDisk=true", "MultiDiskSetup=false"),
			pool:          azurePool(),
			diskSetup:     etcdSetup,
			index:         0,
			expected:      capz.DataDisk{NameSuffix: "etcd", DiskSizeGB: 100, Lun: ptr.To(int32(0))},
		},
		{
			name:          "azure is not enabled by the MultiDiskSetup feature gate",
			installConfig: diskSetupInstallConfig(azurePlatform, "AzureMultiDisk=false", "MultiDiskSetup=true"),
			pool:          azurePool(),
			diskSetup:     etcdSetup,
			index:         0,
			expectedError: `disk setup for azure requires the AzureMultiDisk feature gate to be enabled`,
		},
		{
			name:          "azure without a matching data disk",
			installConfig: diskSetupInstallConfig(azurePlatform, "AzureMultiDisk=true", "MultiDiskSetup=false"),
			pool: func() *types.MachinePool {
				p := azurePool()
				p.Platform.Azure.DataDisks = nil
				return p
			}(),
			diskSetup:     etcdSetup,
			index:         0,
			expectedError: `machine pool "master" disk setup "etcd" has no corresponding Azure data disk`,
		},
		{
			name:          "azure without a machine pool platform",
			installConfig: diskSetupInstallConfig(azurePlatform, "AzureMultiDisk=true", "MultiDiskSetup=false"),
			pool: func() *types.MachinePool {
				p := azurePool()
				p.Platform.Azure = nil
				return p
			}(),
			diskSetup:     etcdSetup,
			index:         0,
			expectedError: `machine pool "master" has a disk setup but no Azure machine pool configuration`,
		},
		{
			name:          "vsphere resolves the data disk by name",
			installConfig: diskSetupInstallConfig(vspherePlatform, "AzureMultiDisk=false", "MultiDiskSetup=true"),
			pool:          vspherePool(),
			diskSetup:     etcdSetup,
			index:         0,
			expected: vsphere.DiskInfo{
				Index: 1,
				Disk:  vspheretypes.DataDisk{Name: "etcd", SizeGiB: 100},
			},
		},
		{
			name:          "vsphere is not enabled by the AzureMultiDisk feature gate",
			installConfig: diskSetupInstallConfig(vspherePlatform, "AzureMultiDisk=true", "MultiDiskSetup=false"),
			pool:          vspherePool(),
			diskSetup:     etcdSetup,
			index:         0,
			expectedError: `disk setup for vsphere requires the MultiDiskSetup feature gate to be enabled`,
		},
		{
			name:          "vsphere without a matching data disk",
			installConfig: diskSetupInstallConfig(vspherePlatform, "AzureMultiDisk=false", "MultiDiskSetup=true"),
			pool: func() *types.MachinePool {
				p := vspherePool()
				p.Platform.VSphere.DataDisks = nil
				return p
			}(),
			diskSetup:     etcdSetup,
			index:         0,
			expectedError: `machine pool "master" disk setup "etcd" has no corresponding vSphere data disk`,
		},
		{
			name:          "unsupported platform",
			installConfig: diskSetupInstallConfig(types.Platform{GCP: &gcptypes.Platform{}}, "AzureMultiDisk=true", "MultiDiskSetup=true"),
			pool: &types.MachinePool{
				Name:      "master",
				DiskSetup: []types.Disk{etcdSetup},
			},
			diskSetup:     etcdSetup,
			index:         0,
			expectedError: `disk setup for gcp is not supported`,
		},
		{
			name:          "unsupported disk setup type",
			installConfig: diskSetupInstallConfig(azurePlatform, "AzureMultiDisk=true", "MultiDiskSetup=false"),
			pool:          azurePool(),
			diskSetup:     types.Disk{Type: "bogus"},
			index:         0,
			expectedError: `disk setup type bogus is not supported`,
		},
		{
			name:          "undefined machine pool",
			installConfig: diskSetupInstallConfig(azurePlatform, "AzureMultiDisk=true", "MultiDiskSetup=false"),
			pool:          nil,
			diskSetup:     etcdSetup,
			index:         0,
			expectedError: `machine pool is not defined`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDisk, err := ResolveDataDisk(tc.installConfig, tc.pool, tc.diskSetup, tc.index)
			if tc.expectedError != "" {
				assert.EqualError(t, err, tc.expectedError)
				assert.Nil(t, dataDisk)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, dataDisk)
		})
	}
}
