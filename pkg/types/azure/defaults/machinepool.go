package defaults

import (
	"sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"

	"github.com/openshift/installer/pkg/types/azure"
)

// cachingTypeNone is the default caching mode for data disks. It matches what
// the Machine API path effectively applies and Microsoft's recommendation for
// write-heavy data disks, and it is the only mode valid for every SKU
// (UltraSSD_LRS requires it). Defaulting it here keeps both provisioning paths
// (CAPZ at install time, Machine API afterwards) on one value: the CAPZ
// webhook would otherwise default an unset cachingType to ReadWrite.
const cachingTypeNone = "None"

// SetMachinePoolDefaults sets the defaults for the platform-specific machine
// pool configuration.
func SetMachinePoolDefaults(p *azure.MachinePool) {
	if p == nil {
		return
	}
	// The DataDisks slice may share its backing array with
	// platform.azure.defaultMachinePlatform: inheritance assigns the slice
	// wholesale (MachinePool.Set), so defaulting an entry in place would leak
	// into the default machine platform and every other pool that inherits
	// it. Default into a private copy instead. The copy also keeps the
	// function idempotent: an explicit value is never revisited.
	needsDefaulting := false
	for _, dataDisk := range p.DataDisks {
		if dataDisk.CachingType == "" {
			needsDefaulting = true
			break
		}
	}
	if !needsDefaulting {
		return
	}
	dataDisks := make([]v1beta1.DataDisk, len(p.DataDisks))
	copy(dataDisks, p.DataDisks)
	for i := range dataDisks {
		if dataDisks[i].CachingType == "" {
			dataDisks[i].CachingType = cachingTypeNone
		}
	}
	p.DataDisks = dataDisks
}
