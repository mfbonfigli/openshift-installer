package machines

import (
	"fmt"

	"github.com/pkg/errors"
	"sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"

	v1 "github.com/openshift/api/machineconfiguration/v1"
	"github.com/openshift/installer/pkg/asset/installconfig"
	"github.com/openshift/installer/pkg/asset/machines/machineconfig"
	"github.com/openshift/installer/pkg/asset/machines/vsphere"
	"github.com/openshift/installer/pkg/types"
	azuretypes "github.com/openshift/installer/pkg/types/azure"
	"github.com/openshift/installer/pkg/types/featuregates"
	vspheretypes "github.com/openshift/installer/pkg/types/vsphere"
)

const (
	// VsphereScsiByPath defines the path format for vsphere disks being added.
	VsphereScsiByPath = "/dev/disk/by-path/pci-0000:03:00.0-scsi-0:0:%d:0"
)

// DiskName returns the platform disk ID for the given disk setup entry.
func DiskName(diskSetup types.Disk) (string, error) {
	switch diskSetup.Type {
	case types.Etcd:
		return diskSetup.Etcd.PlatformDiskID, nil
	case types.Swap:
		return diskSetup.Swap.PlatformDiskID, nil
	case types.UserDefined:
		return diskSetup.UserDefined.PlatformDiskID, nil
	default:
		return "", errors.Errorf("disk setup type %s is not supported", diskSetup.Type)
	}
}

// NodeDiskSetup determines the path per disk type, and per platform and role, runs ForDiskSetup.
func NodeDiskSetup(installConfig *installconfig.InstallConfig, role string, diskSetup types.Disk, dataDisk any) (*v1.MachineConfig, error) {
	var path string

	ic := installConfig.Config

	label := string(diskSetup.Type)

	switch diskSetup.Type {
	case types.Etcd:
		path = "/var/lib/etcd"
	case types.Swap:
		path = ""
	case types.UserDefined:
		path = diskSetup.UserDefined.MountPath
		label = diskSetup.UserDefined.PlatformDiskID
	}

	switch ic.Platform.Name() {
	case azuretypes.Name:
		if azureDataDisk, ok := dataDisk.(v1beta1.DataDisk); ok {
			device := fmt.Sprintf("/dev/disk/azure/scsi1/lun%d", *azureDataDisk.Lun)
			diskSetupIgn, err := machineconfig.ForDiskSetup(role, device, label, path, diskSetup.Type)
			if err != nil {
				return nil, errors.Wrap(err, "failed to create ignition to setup disks for master machines")
			}
			return diskSetupIgn, nil
		}
		return nil, errors.Errorf("unsupported azure data disk type")
	case vspheretypes.Name:
		if vsphereDataDisk, ok := dataDisk.(vsphere.DiskInfo); ok {
			device := fmt.Sprintf(VsphereScsiByPath, vsphereDataDisk.Index+1)
			diskSetupIgn, err := machineconfig.ForDiskSetup(role, device, label, path, diskSetup.Type)
			if err != nil {
				return nil, errors.Wrap(err, "failed to create ignition to setup disks for master machines")
			}
			return diskSetupIgn, nil
		}
		return nil, errors.Errorf("unsupported vsphere data disk type")
	default:
		return nil, errors.Errorf("unsupported platform %q", ic.Platform.Name())
	}
}

// ResolveDataDisk returns the platform-specific data disk that the given disk setup entry
// refers to: Azure disk setups are matched to data disks by position, vSphere ones by name.
// It returns an error when the platform does not support disk setup, when the feature gate
// guarding disk setup on that platform is not enabled, or when no matching data disk exists.
func ResolveDataDisk(installConfig *installconfig.InstallConfig, pool *types.MachinePool, diskSetup types.Disk, index int) (any, error) {
	if pool == nil {
		return nil, errors.Errorf("machine pool is not defined")
	}

	platformName := installConfig.Config.Platform.Name()

	if gate := featuregates.DiskSetupFeatureGate(platformName); !installConfig.Config.Enabled(gate) {
		return nil, errors.Errorf("disk setup for %s requires the %s feature gate to be enabled", platformName, gate)
	}

	// Also validates that the disk setup type is supported.
	diskName, err := DiskName(diskSetup)
	if err != nil {
		return nil, err
	}

	switch platformName {
	case azuretypes.Name:
		if pool.Platform.Azure == nil {
			return nil, errors.Errorf("machine pool %q has a disk setup but no Azure machine pool configuration", pool.Name)
		}
		// Azure data disks are matched to disk setup entries by index. The install config
		// validation guarantees that there is at least one data disk per disk setup entry.
		if index >= len(pool.Platform.Azure.DataDisks) {
			return nil, errors.Errorf("machine pool %q disk setup %q has no corresponding Azure data disk", pool.Name, diskName)
		}
		return pool.Platform.Azure.DataDisks[index], nil
	case vspheretypes.Name:
		if pool.Platform.VSphere == nil {
			return nil, errors.Errorf("machine pool %q has a disk setup but no vSphere machine pool configuration", pool.Name)
		}
		for diskIndex, disk := range pool.Platform.VSphere.DataDisks {
			if disk.Name == diskName {
				return vsphere.DiskInfo{
					Index: diskIndex,
					Disk:  disk,
				}, nil
			}
		}
		return nil, errors.Errorf("machine pool %q disk setup %q has no corresponding vSphere data disk", pool.Name, diskName)
	default:
		return nil, errors.Errorf("disk setup for %s is not supported", platformName)
	}
}
