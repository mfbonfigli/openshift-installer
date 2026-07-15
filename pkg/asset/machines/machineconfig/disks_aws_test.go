package machineconfig

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	"github.com/openshift/installer/data"
	"github.com/openshift/installer/pkg/types"
)

func TestMain(m *testing.M) {
	_, src, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(src), "..", "..", "..", "..")
	data.Assets = http.Dir(filepath.Join(repoRoot, "data", "data"))
	os.Exit(m.Run())
}

func TestForDiskSetupAWSPlatform(t *testing.T) {
	cases := []struct {
		name     string
		role     string
		device   string
		label    string
		path     string
		diskType types.DiskType
	}{
		{
			name:     "etcd disk setup",
			role:     "master",
			device:   "/dev/xvdb",
			label:    "etcd",
			path:     "/var/lib/etcd",
			diskType: types.Etcd,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mc, err := ForDiskSetup(tc.role, "aws", tc.device, tc.label, tc.path, tc.diskType)
			if !assert.NoError(t, err) || !assert.NotNil(t, mc) {
				return
			}

			assert.Equal(t, metav1.TypeMeta{
				APIVersion: mcfgv1.SchemeGroupVersion.String(),
				Kind:       "MachineConfig",
			}, mc.TypeMeta)
			assert.Equal(t, tc.role, mc.ObjectMeta.Labels["machineconfiguration.openshift.io/role"])

			ignConfig, err := parseIgnitionConfig(mc.Spec.Config.Raw)
			if !assert.NoError(t, err) {
				return
			}

			assert.Empty(t, ignConfig.Storage.Disks, "AWS disk setup must not use ignition storage.disks")
			assert.Empty(t, ignConfig.Storage.Filesystems, "AWS disk setup must not use ignition storage.filesystems")

			if !assert.Len(t, ignConfig.Storage.Files, 1, "should have the setup script") {
				return
			}
			assert.Equal(t, "/usr/local/bin/aws-nvme-disk-setup.sh", ignConfig.Storage.Files[0].Path)
			assert.NotNil(t, ignConfig.Storage.Files[0].Mode)
			assert.Equal(t, 0755, *ignConfig.Storage.Files[0].Mode)

			assertAWSEtcdUnits(t, ignConfig, tc.label)
		})
	}
}

func TestForDiskSetupAWSPlatformSetupServiceContent(t *testing.T) {
	mc, err := ForDiskSetup("master", "aws", "/dev/xvdb", "etcd", "/var/lib/etcd", types.Etcd)
	if !assert.NoError(t, err) {
		return
	}

	ignConfig, err := parseIgnitionConfig(mc.Spec.Config.Raw)
	if !assert.NoError(t, err) {
		return
	}

	var setupUnit string
	for _, u := range ignConfig.Systemd.Units {
		if u.Name == "aws-disk-setup-etcd.service" {
			setupUnit = *u.Contents
			break
		}
	}
	if !assert.NotEmpty(t, setupUnit, "setup service unit not found") {
		return
	}

	assert.Contains(t, setupUnit, "After=ostree-remount.service")
	assert.Contains(t, setupUnit, "ExecStart=/usr/local/bin/aws-nvme-disk-setup.sh xvdb etcd")
	assert.Contains(t, setupUnit, "Type=oneshot")
	assert.Contains(t, setupUnit, "RemainAfterExit=yes")
}

func TestForDiskSetupAWSPlatformDeviceNameStripping(t *testing.T) {
	cases := []struct {
		name         string
		deviceName   string
		expectedBdev string
	}{
		{
			name:         "xvd device name stripped to bdev",
			deviceName:   "/dev/xvdb",
			expectedBdev: "xvdb",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mc, err := ForDiskSetup("master", "aws", tc.deviceName, "etcd", "/var/lib/etcd", types.Etcd)
			if !assert.NoError(t, err) {
				return
			}

			ignConfig, err := parseIgnitionConfig(mc.Spec.Config.Raw)
			if !assert.NoError(t, err) {
				return
			}

			var setupUnit string
			for _, u := range ignConfig.Systemd.Units {
				if u.Name == "aws-disk-setup-etcd.service" {
					setupUnit = *u.Contents
					break
				}
			}
			if !assert.NotEmpty(t, setupUnit) {
				return
			}
			assert.Contains(t, setupUnit, tc.expectedBdev+" etcd")
		})
	}
}

func assertAWSEtcdUnits(t *testing.T, ignConfig *igntypes.Config, label string) {
	t.Helper()

	if !assert.Len(t, ignConfig.Systemd.Units, 2, "should have setup service + mount unit") {
		return
	}

	var hasSetupService, hasMountUnit bool
	for _, u := range ignConfig.Systemd.Units {
		switch u.Name {
		case "aws-disk-setup-" + label + ".service":
			hasSetupService = true
			assert.True(t, *u.Enabled)
		case "var-lib-etcd.mount":
			hasMountUnit = true
			assert.True(t, *u.Enabled)
			assert.Contains(t, *u.Contents, "What=/dev/disk/by-partlabel/"+label)
			assert.Contains(t, *u.Contents, "Where=/var/lib/etcd")
			assert.Contains(t, *u.Contents, "Type=xfs")
		}
	}
	assert.True(t, hasSetupService, "setup service unit not found")
	assert.True(t, hasMountUnit, "mount unit not found")
}

