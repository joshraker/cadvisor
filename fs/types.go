// Copyright 2014 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fs

import "time"

type partition struct {
	mountpoint string
	major      uint
	minor      uint
	fsType     string
	blockSize  uint
}

type DeviceInfo struct {
	Device string
	Major  uint
	Minor  uint
}

type FsType string

func (ft FsType) String() string {
	return string(ft)
}

const (
	ZFS          FsType = "zfs"
	DeviceMapper FsType = "devicemapper"
	VFS          FsType = "vfs"
)

type Fs struct {
	DeviceInfo
	Type       FsType
	Capacity   uint64
	Free       uint64
	Available  uint64
	Inodes     uint64
	InodesFree uint64
	DiskStats  DiskStats
}

type DiskStats struct {
	ReadsCompleted  uint64
	ReadsMerged     uint64
	SectorsRead     uint64
	ReadTime        uint64
	WritesCompleted uint64
	WritesMerged    uint64
	SectorsWritten  uint64
	WriteTime       uint64
	IoInProgress    uint64
	IoTime          uint64
	WeightedIoTime  uint64
}

type FsInfo interface {
	// Refreshes the cache
	RefreshCache()

	// Returns capacity and free space, in bytes, of all the ext2, ext3, ext4 filesystems on the host.
	GetGlobalFsInfo(withIoStats bool) ([]Fs, error)

	// Returns capacity and free space, in bytes, of the set of mounts passed.
	GetFsInfoForMounts(mountSet map[string]struct{}, withIoStats bool) ([]Fs, error)

	// Returns capacity and free space, in bytes, of the set of devices passed.
	GetFsInfoForDevices(deviceSet map[string]struct{}, withIoStats bool) ([]Fs, error)

	// Returns number of bytes occupied by 'dir'.
	GetDirUsage(dir string, timeout time.Duration) (uint64, error)

	// Returns the block device info of the filesystem on which 'dir' resides.
	GetDirFsDevice(dir string) (*DeviceInfo, error)

	// Returns the device name associated with a particular label.
	GetDeviceForLabel(label string) (string, error)

	// Returns all labels associated with a particular device name.
	GetLabelsForDevice(device string) ([]string, error)

	// Returns the mountpoint associated with a particular device.
	GetMountpointForDevice(device string) (string, error)
}

type PartitionCache interface {
	Refresh() error
	Clear()
	PartitionForDevice(device string) (partition, error)
	DeviceInfoForMajorMinor(major uint, minor uint) (*DeviceInfo, error)
	ApplyOverPartitions(f func(device string, p partition) error) error
	DeviceNameForLabel(label string) (string, error)
	ApplyOverLabels(f func(label string, device string) error) error
}

type FsStatsCache interface {
	Clear()
	FsStats(dev string, part partition) (FsType, uint64, uint64, uint64, uint64, uint64, error)
}
