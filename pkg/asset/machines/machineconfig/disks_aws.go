package machineconfig

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/template"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
	"k8s.io/utils/ptr"

	"github.com/openshift/installer/data"
)

const awsNVMeSetupScriptPath = "machineconfig/aws-nvme-disk-setup.sh"

const awsSetupServiceTemplate = `[Unit]
Description=Setup AWS NVMe disk for {{.Label}}
DefaultDependencies=no
After=ostree-remount.service
Before={{.BeforeUnit}}

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/aws-nvme-disk-setup.sh {{.TargetBdev}} {{.Label}}

[Install]
RequiredBy={{.BeforeUnit}}
`

type awsSetupParams struct {
	Label      string
	TargetBdev string
	BeforeUnit string
}

// addAWSNVMeDiskSetup adds the NVMe device discovery script and a systemd
// oneshot service to the ignition config. This replaces ignition storage.disks
// because /dev/sd* paths are not available during the initramfs stage on Nitro
// instances — the script runs after udev settles and uses "nvme amzn id-ctrl"
// to find the correct NVMe device by its block device mapping name.
func addAWSNVMeDiskSetup(ignConfig *igntypes.Config, device, label, path string) error {
	scriptFile, err := data.Assets.Open(awsNVMeSetupScriptPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", awsNVMeSetupScriptPath, err)
	}
	defer scriptFile.Close()
	scriptBytes, err := io.ReadAll(scriptFile)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", awsNVMeSetupScriptPath, err)
	}

	scriptMode := 0755
	ignConfig.Storage.Files = append(ignConfig.Storage.Files, igntypes.File{
		Node: igntypes.Node{
			Path: "/usr/local/bin/aws-nvme-disk-setup.sh",
		},
		FileEmbedded1: igntypes.FileEmbedded1{
			Mode: &scriptMode,
			Contents: igntypes.Resource{
				Source: ptr.To(dataurl.EncodeBytes(scriptBytes)),
			},
		},
	})

	targetBdev := strings.TrimPrefix(device, "/dev/")

	unitName := strings.Trim(path, "/")
	unitName = strings.ReplaceAll(unitName, "/", "-")
	beforeUnit := fmt.Sprintf("%s.mount", unitName)

	params := awsSetupParams{
		Label:      label,
		TargetBdev: targetBdev,
		BeforeUnit: beforeUnit,
	}
	tmpl, err := template.New("awsSetup").Parse(awsSetupServiceTemplate)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return err
	}
	contents := buf.String()

	ignConfig.Systemd.Units = append(ignConfig.Systemd.Units, igntypes.Unit{
		Name:     fmt.Sprintf("aws-disk-setup-%s.service", strings.ToLower(label)),
		Enabled:  ptr.To(true),
		Contents: &contents,
	})

	return nil
}
