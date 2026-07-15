package machineconfig

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	"github.com/openshift/installer/pkg/asset/ignition"
	"github.com/openshift/installer/pkg/types"
	awstypes "github.com/openshift/installer/pkg/types/aws"
)

// diskMount is used to supply the template the proper fields to produce the unit string.
// It contains the mount path and partition label for generating systemd mount units.
type diskMount struct {
	MountPath string
	Label     string
}

const diskMountUnit = `
[Unit]
Requires=systemd-fsck@dev-disk-by\x2dpartlabel-{{.Label}}.service
After=systemd-fsck@dev-disk-by\x2dpartlabel-{{.Label}}.service

[Mount]
Where={{.MountPath}}
What=/dev/disk/by-partlabel/{{.Label}}
Type=xfs
Options=defaults,prjquota

[Install]
RequiredBy=local-fs.target
`

const swapMountUnit = `
[Swap]
What=/dev/disk/by-partlabel/{{.Label}}

[Install]
WantedBy=swap.target
`

const gptSwap = "0657FD6D-A4AB-43C4-84E5-0933C84B4F4F"

// ForDiskSetup generates a MachineConfig for disk setup based on the specified disk type.
// It supports three disk setup types: etcd, swap, and user-defined disks.
//
// Parameters:
//   - role: the machine role (e.g., "master", "worker") that this config applies to
//   - platform: the platform name (e.g., "aws", "azure"). On AWS, disk setup uses a
//     systemd service to discover the NVMe device instead of ignition storage.disks,
//     because /dev/sd* paths are not available during the initramfs stage on Nitro instances.
//   - device: the device path (e.g., "/dev/sdb") where the disk will be configured
//   - label: the partition label to assign to the disk partition
//   - path: the mount path for the disk (not used for swap disks)
//   - diskType: the type of disk configuration (types.Etcd, types.Swap, or types.UserDefined)
func ForDiskSetup(role, platform, device, label, path string, diskType types.DiskType) (*mcfgv1.MachineConfig, error) {
	ignConfig := igntypes.Config{
		Ignition: igntypes.Ignition{
			Version: igntypes.MaxVersion.String(),
		},
	}

	// Remove all non-alphanumeric characters from the label
	reg := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	label = reg.ReplaceAllString(label, "")

	if platform == awstypes.Name {
		if err := addAWSNVMeDiskSetup(&ignConfig, device, label, path); err != nil {
			return nil, err
		}
	} else {
		if err := addIgnitionDiskSetup(&ignConfig, device, label, path, diskType); err != nil {
			return nil, err
		}
	}

	if err := addMountUnits(&ignConfig, label, path, diskType); err != nil {
		return nil, err
	}

	rawExt, err := ignition.ConvertToRawExtension(ignConfig)
	if err != nil {
		return nil, err
	}

	return &mcfgv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: mcfgv1.SchemeGroupVersion.String(),
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("01-disk-setup-%s-%s", strings.ToLower(label), role),
			Labels: map[string]string{
				"machineconfiguration.openshift.io/role": role,
			},
		},
		Spec: mcfgv1.MachineConfigSpec{
			Config: rawExt,
		},
	}, nil
}

func addIgnitionDiskSetup(ignConfig *igntypes.Config, device, label, path string, diskType types.DiskType) error {
	partition := igntypes.Partition{
		Label:    ptr.To(label),
		StartMiB: ptr.To(0),
		SizeMiB:  ptr.To(0),
	}
	if diskType == types.Swap {
		partition.GUID = ptr.To(gptSwap)
	}
	ignConfig.Storage.Disks = append(ignConfig.Storage.Disks, igntypes.Disk{
		Device:     device,
		Partitions: []igntypes.Partition{partition},
		WipeTable:  ptr.To(true),
	})

	fs := igntypes.Filesystem{
		Device: fmt.Sprintf("/dev/disk/by-partlabel/%s", label),
		Label:  ptr.To(label),
	}
	switch diskType {
	case types.Etcd, types.UserDefined:
		fs.Format = ptr.To("xfs")
		fs.Path = ptr.To(path)
		fs.MountOptions = []igntypes.MountOption{"defaults", "prjquota"}
		fs.WipeFilesystem = ptr.To(true)
	case types.Swap:
		fs.Format = ptr.To("swap")
	}
	ignConfig.Storage.Filesystems = append(ignConfig.Storage.Filesystems, fs)
	return nil
}

func addMountUnits(ignConfig *igntypes.Config, label, path string, diskType types.DiskType) error {
	mountData := diskMount{
		MountPath: path,
		Label:     label,
	}

	switch diskType {
	case types.Etcd, types.UserDefined:
		tmpl, err := template.New("mountUnit").Parse(diskMountUnit)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, mountData); err != nil {
			return err
		}
		contents := buf.String()

		unitName := strings.Trim(path, "/")
		unitName = strings.ReplaceAll(unitName, "/", "-")
		ignConfig.Systemd.Units = append(ignConfig.Systemd.Units, igntypes.Unit{
			Name:     fmt.Sprintf("%s.mount", unitName),
			Enabled:  ptr.To(true),
			Contents: &contents,
		})

	case types.Swap:
		tmpl, err := template.New("swapUnit").Parse(swapMountUnit)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, mountData); err != nil {
			return err
		}
		contents := buf.String()

		ignConfig.Systemd.Units = append(ignConfig.Systemd.Units, igntypes.Unit{
			Name:     "dev-disk-by\\x2dpartlabel-swap.swap",
			Enabled:  ptr.To(true),
			Contents: &contents,
		})
	}
	return nil
}
