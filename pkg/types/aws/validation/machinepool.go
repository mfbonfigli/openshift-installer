package validation

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/openshift/installer/pkg/types"
	"github.com/openshift/installer/pkg/types/aws"
)

var (
	validArchitectures = map[types.Architecture]bool{
		types.ArchitectureAMD64: true,
		types.ArchitectureARM64: true,
	}

	// validArchitectureValues lists the supported arches for AWS.
	validArchitectureValues = func() []string {
		v := make([]string, 0, len(validArchitectures))
		for m := range validArchitectures {
			v = append(v, string(m))
		}
		return v
	}()

	validMetadataAuthValues = sets.NewString("Required", "Optional")

	validConfidentialComputePolicy = []aws.ConfidentialComputePolicy{
		aws.ConfidentialComputePolicyDisabled,
		aws.ConfidentialComputePolicySEVSNP,
	}
)

// AWS has a limit of 16 security groups. See:
// https://docs.aws.amazon.com/vpc/latest/userguide/amazon-vpc-limits.html
// We set a user limit of 10 and reserve 6 for use by OpenShift.
const maxUserSecurityGroupsCount = 10

// maxIopsThroughputRatio is the minimum allowed ratio of IOPS to throughput (MiBps) for gp3 volumes.
// AWS constraint: throughput (MiBps) / iops <= 0.25 (maximum 0.25 MiBps per iops) --> iops / throughput (MiBps) >= 4
// This constant is used for integer comparison to avoid floating point precision issues.
const maxIopsThroughputRatio = 4

// gp3DefaultIOPS is the default IOPS value for gp3 volumes when not explicitly set.
// According to AWS documentation, gp3 volumes have a baseline of 3,000 IOPS.
const gp3DefaultIOPS int32 = 3000

// ValidateMachinePool checks that the specified machine pool is valid.
func ValidateMachinePool(platform *aws.Platform, p *aws.MachinePool, poolName string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	for i, zone := range p.Zones {
		if !strings.HasPrefix(zone, platform.Region) {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("zones").Index(i), zone, fmt.Sprintf("Zone not in configured region (%s)", platform.Region)))
		}
	}

	if p.EC2RootVolume.Type != "" {
		allErrs = append(allErrs, validateVolumeSize(p, fldPath)...)
		allErrs = append(allErrs, validateIOPS(p, fldPath)...)
		allErrs = append(allErrs, validateThroughput(p, fldPath)...)
	}

	if p.EC2Metadata.Authentication != "" && !validMetadataAuthValues.Has(p.EC2Metadata.Authentication) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("authentication"), p.EC2Metadata.Authentication, "must be either Required or Optional"))
	}

	allErrs = append(allErrs, validateSecurityGroups(platform, p, fldPath)...)
	allErrs = append(allErrs, ValidateCPUOptions(p, fldPath)...)
	allErrs = append(allErrs, validateHostPlacement(p, poolName, fldPath.Child("hostPlacement"))...)
	allErrs = append(allErrs, validateNonRootVolumes(p, poolName, fldPath.Child("nonRootVolumes"))...)

	return allErrs
}

func validateHostPlacement(p *aws.MachinePool, poolName string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	// No HostPlacement, so lets just return
	if p.HostPlacement == nil {
		return allErrs
	}

	// Dedicated hosts are only supported on worker (compute) pools
	if poolName != "" && poolName != types.MachinePoolComputeRoleName {
		errMsg := fmt.Sprintf("dedicated hosts are only supported on %s pools, not on %s pools", types.MachinePoolComputeRoleName, poolName)
		allErrs = append(allErrs, field.Invalid(fldPath, p.HostPlacement, errMsg))
		return allErrs
	}

	// Control plane pools cannot use dedicated hosts
	if poolName == "" {
		errMsg := "dedicated hosts are not supported on control plane pools"
		allErrs = append(allErrs, field.Invalid(fldPath, p.HostPlacement, errMsg))
		return allErrs
	}

	if p.HostPlacement.Affinity == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("affinity"), "affinity is required when hostPlacement is configured"))
		return allErrs // Can't validate further without affinity
	}

	switch *p.HostPlacement.Affinity {
	case aws.HostAffinityAnyAvailable:
		if len(p.HostPlacement.DedicatedHost) > 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("dedicatedHost"), "dedicatedHost is required when 'affinity' is set to DedicatedHost, and forbidden otherwise"))
		}
	case aws.HostAffinityDedicatedHost:
		if len(p.HostPlacement.DedicatedHost) == 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("dedicatedHost"), "dedicatedHost is required when 'affinity' is set to DedicatedHost, and forbidden otherwise"))
		} else {
			for index, host := range p.HostPlacement.DedicatedHost {
				hostPath := fldPath.Child("dedicatedHost").Index(index)
				if len(host.ID) == 0 {
					allErrs = append(allErrs, field.Required(hostPath.Child("id"), "a hostID must be specified when configuring 'dedicatedHost'"))
				}
			}
		}
	default:
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("affinity"), p.HostPlacement.Affinity, []aws.HostAffinity{aws.HostAffinityAnyAvailable, aws.HostAffinityDedicatedHost}))
	}

	return allErrs
}

func validateSecurityGroups(platform *aws.Platform, p *aws.MachinePool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(p.AdditionalSecurityGroupIDs) > 0 && len(platform.VPC.Subnets) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("platform.subnets"), "subnets must be provided when additional security groups are present"))
	}

	// The installer also creates a security group: `${var.cluster_id}-master-sg/${var.cluster_id}-worker-sg`
	if count := len(p.AdditionalSecurityGroupIDs); count > maxUserSecurityGroupsCount {
		allErrs = append(allErrs, field.TooMany(fldPath, count, maxUserSecurityGroupsCount))
	}

	return allErrs
}

func validateVolumeSize(p *aws.MachinePool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	volumeSize := p.EC2RootVolume.Size

	if volumeSize < 0 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("size"), volumeSize, "volume size value must be a positive number"))
	}

	return allErrs
}

func validateIOPS(p *aws.MachinePool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	volumeType := strings.ToLower(p.EC2RootVolume.Type)
	iops := p.EC2RootVolume.IOPS

	switch volumeType {
	case "io1", "io2":
		if iops < 0 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("iops"), iops, "iops must be a positive number"))
		}
	case "gp3":
		if iops < 0 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("iops"), iops, "iops must be a positive number"))
		}
	case "gp2", "st1", "sc1", "standard":
		if iops != 0 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("iops"), iops, fmt.Sprintf("iops not supported for type %s", volumeType)))
		}
	default:
		allErrs = append(allErrs, field.Invalid(fldPath.Child("type"), volumeType, fmt.Sprintf("failed to find volume type %s", volumeType)))
	}

	return allErrs
}

func validateThroughput(p *aws.MachinePool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if p.EC2RootVolume.Throughput == nil {
		return allErrs
	}

	volumeType := strings.ToLower(p.EC2RootVolume.Type)
	throughput := *p.EC2RootVolume.Throughput

	switch volumeType {
	case "gp3":
		if throughput < 125 || throughput > 2000 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("throughput"), throughput, "throughput must be between 125 MiB/s and 2000 MiB/s"))
			return allErrs
		}
		// AWS constraint: throughput (MiBps) / iops <= 0.25 (maximum 0.25 MiBps per iops)
		// When iops is 0 or omitted, AWS defaults to 3000 iops
		// Validate that the throughput/iops ratio does not exceed the maximum allowed ratio
		iops := int32(p.EC2RootVolume.IOPS)
		if iops == 0 {
			// Use AWS default of 3000 iops when iops is not set
			iops = gp3DefaultIOPS
		}
		// Use integer comparison to avoid floating point precision issues
		// throughput / iops <= 0.25 is equivalent to throughput * maxIopsThroughputRatio <= iops
		if throughput*maxIopsThroughputRatio > iops {
			// Calculate minimum required iops: iops >= throughput / 0.25
			// Round up to nearest 100 for safety
			calculatedIOPS := ((throughput * maxIopsThroughputRatio) + 99) / 100 * 100
			// According to AWS documentation, gp3 volumes have a baseline of 3,000 IOPS
			minIOPS := max(calculatedIOPS, gp3DefaultIOPS)
			// Calculate ratio for error message display
			ratio := float32(throughput) / float32(iops)
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("throughput"),
				throughput,
				fmt.Sprintf("throughput (MiBps) to iops ratio of %.6f is too high; maximum is %.6f MiBps per iops. When iops is not set, AWS defaults to %d iops. Please set iops to at least %d to satisfy the constraint", ratio, 1.0/maxIopsThroughputRatio, gp3DefaultIOPS, minIOPS),
			))
		}
	default:
		allErrs = append(allErrs, field.Invalid(fldPath.Child("throughput"), throughput, fmt.Sprintf("throughput not supported for type %s", volumeType)))
	}

	return allErrs
}

// validDeviceNamePattern restricts device names to the /dev/xvd[b-z] form.
// This single convention works across both AWS hypervisor generations:
// on Nitro instances AWS populates the NVMe controller bdev field with
// the xvd-prefixed name; on Xen instances the device appears directly
// at that path.
var validDeviceNamePattern = regexp.MustCompile(`^/dev/xvd[b-z]$`)

var validVolumeTypes = sets.NewString("gp2", "gp3", "io1", "io2", "st1", "sc1", "standard")

var rootDeviceNames = sets.NewString("/dev/xvda")

func validateNonRootVolumes(p *aws.MachinePool, poolName string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(p.NonRootVolumes) == 0 {
		return allErrs
	}

	if poolName != types.MachinePoolControlPlaneRoleName {
		allErrs = append(allErrs, field.Invalid(fldPath, p.NonRootVolumes, "nonRootVolumes are only supported on the control plane machine pool"))
		return allErrs
	}

	deviceNames := sets.NewString()
	for i, vol := range p.NonRootVolumes {
		volPath := fldPath.Index(i)

		if vol.DeviceName == "" {
			allErrs = append(allErrs, field.Required(volPath.Child("deviceName"), "device name is required"))
		} else if rootDeviceNames.Has(vol.DeviceName) {
			allErrs = append(allErrs, field.Invalid(volPath.Child("deviceName"), vol.DeviceName, "must not conflict with the root device"))
		} else if !validDeviceNamePattern.MatchString(vol.DeviceName) {
			allErrs = append(allErrs, field.Invalid(volPath.Child("deviceName"), vol.DeviceName, "must match /dev/xvd[b-z]"))
		} else if deviceNames.Has(vol.DeviceName) {
			allErrs = append(allErrs, field.Invalid(volPath.Child("deviceName"), vol.DeviceName, "device name must be unique across all non-root volumes"))
		} else {
			deviceNames.Insert(vol.DeviceName)
		}

		if vol.Size <= 0 {
			allErrs = append(allErrs, field.Invalid(volPath.Child("size"), vol.Size, "must be greater than zero"))
		}

		volType := strings.ToLower(vol.Type)
		if vol.Type == "" {
			allErrs = append(allErrs, field.Required(volPath.Child("type"), "volume type is required"))
		} else if !validVolumeTypes.Has(volType) {
			allErrs = append(allErrs, field.NotSupported(volPath.Child("type"), vol.Type, validVolumeTypes.List()))
		} else {
			switch volType {
			case "io1", "io2", "gp3":
				if vol.IOPS < 0 {
					allErrs = append(allErrs, field.Invalid(volPath.Child("iops"), vol.IOPS, "iops must be a positive number"))
				}
			default:
				if vol.IOPS != 0 {
					allErrs = append(allErrs, field.Invalid(volPath.Child("iops"), vol.IOPS, fmt.Sprintf("iops not supported for type %s", volType)))
				}
			}

			if vol.Throughput != nil {
				if volType != "gp3" {
					allErrs = append(allErrs, field.Invalid(volPath.Child("throughput"), *vol.Throughput, fmt.Sprintf("throughput not supported for type %s", volType)))
				} else if *vol.Throughput < 125 || *vol.Throughput > 2000 {
					allErrs = append(allErrs, field.Invalid(volPath.Child("throughput"), *vol.Throughput, "throughput must be between 125 MiB/s and 2000 MiB/s"))
				}
			}
		}
	}

	return allErrs
}

// ValidateNonRootVolumesDiskSetup cross-validates the platform-specific NonRootVolumes
// with the platform-agnostic DiskSetup entries.
func ValidateNonRootVolumesDiskSetup(p *aws.MachinePool, pool *types.MachinePool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(p.NonRootVolumes) == 0 || len(pool.DiskSetup) == 0 {
		return allErrs
	}

	if len(p.NonRootVolumes) < len(pool.DiskSetup) {
		allErrs = append(allErrs, field.TooLong(fldPath, pool.DiskSetup, len(p.NonRootVolumes)))
		return allErrs
	}

	deviceNameSet := sets.NewString()
	for _, vol := range p.NonRootVolumes {
		deviceNameSet.Insert(vol.DeviceName)
	}

	for i, setup := range pool.DiskSetup {
		var platformDiskID string
		switch setup.Type {
		case types.Etcd:
			if setup.Etcd != nil {
				platformDiskID = setup.Etcd.PlatformDiskID
			}
		case types.Swap:
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("diskSetup").Index(i).Child("type"),
				setup.Type,
				"swap disk setup is not yet supported on AWS",
			))
			continue
		case types.UserDefined:
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("diskSetup").Index(i).Child("type"),
				setup.Type,
				"userDefined disk setup is not supported on AWS",
			))
			continue
		}

		if platformDiskID != "" && !deviceNameSet.Has(platformDiskID) {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("diskSetup").Index(i).Child("platformDiskID"),
				platformDiskID,
				"must match a nonRootVolumes[].deviceName",
			))
		}
	}

	return allErrs
}

// ValidateAMIID check the AMI ID is set for a machine pool.
func ValidateAMIID(platform *aws.Platform, p *aws.MachinePool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	pool := &aws.MachinePool{AMIID: platform.AMIID}
	pool.Set(platform.DefaultMachinePlatform)
	pool.Set(p)

	// regions is a list of regions for which the user should set AMI ID as copying the AMI to these regions
	// is known to not be supported.
	regions := sets.NewString("us-iso-east-1", "us-isob-east-1", "us-iso-west-1", "cn-north-1", "cn-northwest-1")
	if pool.AMIID == "" && regions.Has(platform.Region) {
		allErrs = append(allErrs, field.Required(fldPath, fmt.Sprintf("AMI ID must be provided for regions %s", strings.Join(regions.List(), ", "))))
	}
	return allErrs
}

// ValidateMachinePoolArchitecture checks that a valid architecture is set for a machine pool.
func ValidateMachinePoolArchitecture(pool *types.MachinePool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if !validArchitectures[pool.Architecture] {
		allErrs = append(allErrs, field.NotSupported(fldPath, pool.Architecture, validArchitectureValues))
	}
	return allErrs
}

// ValidateCPUOptions checks that valid CPU options are set for a machine pool.
func ValidateCPUOptions(p *aws.MachinePool, fldPath *field.Path) field.ErrorList {
	if p.CPUOptions == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if *p.CPUOptions == (aws.CPUOptions{}) {
		allErrs = append(
			allErrs,
			field.Invalid(
				fldPath.Child("cpuOptions"),
				"{}",
				"At least one field must be set if cpuOptions is provided",
			),
		)
	}

	if p.CPUOptions.ConfidentialCompute != nil {
		switch *p.CPUOptions.ConfidentialCompute {
		case aws.ConfidentialComputePolicyDisabled, aws.ConfidentialComputePolicySEVSNP:
			// Valid values
		default:
			allErrs = append(
				allErrs,
				field.NotSupported(
					fldPath.Child("confidentialCompute"),
					p.CPUOptions.ConfidentialCompute,
					validConfidentialComputePolicy,
				),
			)
		}
	}

	return allErrs
}
