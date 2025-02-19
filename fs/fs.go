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

// +build linux

// Provides Filesystem Stats
package fs

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"
)

type RealFsInfo struct {
	partitionCache PartitionCache
	fsStatsCache   FsStatsCache
}

type Context struct {
	// docker root directory.
	Docker  DockerContext
	RktPath string
}

type DockerContext struct {
	Root         string
	Driver       string
	DriverStatus map[string]string
}

func NewFsInfo(context Context) (FsInfo, error) {
	fsInfo := &RealFsInfo{
		partitionCache: NewPartitionCache(context),
		fsStatsCache:   NewFsStatsCache(),
	}

	glog.Infof("Listing filesystem partitions:")
	fsInfo.partitionCache.ApplyOverPartitions(func(d string, p partition) error {
		glog.Infof("%s: %+v", d, p)
		return nil
	})

	return fsInfo, nil
}

func (self *RealFsInfo) RefreshCache() {
	err := self.partitionCache.Refresh()
	if err != nil {
		glog.Warningf("Failed to refresh partition cache: %s")
	}
}

func (self *RealFsInfo) GetDeviceForLabel(label string) (string, error) {
	return self.partitionCache.DeviceNameForLabel(label)
}

func (self *RealFsInfo) GetLabelsForDevice(device string) ([]string, error) {
	labels := make([]string, 0)
	self.partitionCache.ApplyOverLabels(func(label string, deviceForLabel string) error {
		if device == deviceForLabel {
			labels = append(labels, label)
		}
		return nil
	})
	return labels, nil
}

func (self *RealFsInfo) GetMountpointForDevice(dev string) (string, error) {
	p, err := self.partitionCache.PartitionForDevice(dev)
	if err != nil {
		return "", err
	}
	return p.mountpoint, nil
}

func (self *RealFsInfo) getFilteredFsInfo(filter func(_ string, _ partition) bool, withIoStats bool) ([]Fs, error) {
	filesystemsOut := make([]Fs, 0)

	err := self.partitionCache.ApplyOverPartitions(func(device string, partition partition) error {
		if !filter(device, partition) {
			return nil
		}

		var (
			fs  Fs
			err error
		)

		fs.Type, fs.Capacity, fs.Free, fs.Available, fs.Inodes, fs.InodesFree, err = self.fsStatsCache.FsStats(device, partition)

		if err != nil {
			// Only log, don't return an error, move on to the next FS
			glog.Errorf("Stat fs for %q failed. Error: %v", device, err)
			return nil
		}

		fs.DeviceInfo = DeviceInfo{
			Device: device,
			Major:  uint(partition.major),
			Minor:  uint(partition.minor),
		}

		filesystemsOut = append(filesystemsOut, fs)
		return nil
	})

	if err != nil {
		return nil, err
	}

	// TODO: Use a cache here as well?
	if withIoStats {
		diskStatsMap, err := getDiskStatsMap("/proc/diskstats")
		if err != nil {
			return nil, err
		}

		for _, fs := range filesystemsOut {
			diskStats, ok := diskStatsMap[fs.DeviceInfo.Device]
			if !ok {
				// TODO: ecryptfs breaks with this, since the disk stats we should
				// report are the disk stats for the underlying physical volume, not
				// the ecryptfs one. We should (probably) handle ecryptfs a little
				// differently here, and look at the disk stats for the lower layer.
				// glog.Warningf("Disk stats for %q not found", fs.DeviceInfo.Device)
				continue
			}
			fs.DiskStats = diskStats
		}
	}

	return filesystemsOut, nil
}

func (self *RealFsInfo) GetFsInfoForMounts(mountSet map[string]struct{}, withIoStats bool) ([]Fs, error) {
	return self.getFilteredFsInfo(func(_ string, partition partition) bool {
		_, hasMount := mountSet[partition.mountpoint]
		return hasMount
	}, withIoStats)
}

func (self *RealFsInfo) GetFsInfoForDevices(deviceSet map[string]struct{}, withIoStats bool) ([]Fs, error) {
	return self.getFilteredFsInfo(func(device string, _ partition) bool {
		_, hasDevice := deviceSet[device]
		return hasDevice
	}, withIoStats)
}

func (self *RealFsInfo) GetGlobalFsInfo(withIoStats bool) ([]Fs, error) {
	return self.getFilteredFsInfo(func(_ string, _ partition) bool {
		return true
	}, withIoStats)
}

var partitionRegex = regexp.MustCompile(`^(?:(?:s|xv)d[a-z]+\d*|dm-\d+)$`)

func getDiskStatsMap(diskStatsFile string) (map[string]DiskStats, error) {
	diskStatsMap := make(map[string]DiskStats)
	file, err := os.Open(diskStatsFile)
	if err != nil {
		if os.IsNotExist(err) {
			glog.Infof("not collecting filesystem statistics because file %q was not available", diskStatsFile)
			return diskStatsMap, nil
		}
		return nil, err
	}

	defer file.Close()
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		words := strings.Fields(line)
		if !partitionRegex.MatchString(words[2]) {
			continue
		}
		// 8      50 sdd2 40 0 280 223 7 0 22 108 0 330 330
		deviceName := path.Join("/dev", words[2])
		wordLength := len(words)
		offset := 3
		var stats = make([]uint64, wordLength-offset)
		if len(stats) < 11 {
			return nil, fmt.Errorf("could not parse all 11 columns of /proc/diskstats")
		}
		var error error
		for i := offset; i < wordLength; i++ {
			stats[i-offset], error = strconv.ParseUint(words[i], 10, 64)
			if error != nil {
				return nil, error
			}
		}
		diskStats := DiskStats{
			ReadsCompleted:  stats[0],
			ReadsMerged:     stats[1],
			SectorsRead:     stats[2],
			ReadTime:        stats[3],
			WritesCompleted: stats[4],
			WritesMerged:    stats[5],
			SectorsWritten:  stats[6],
			WriteTime:       stats[7],
			IoInProgress:    stats[8],
			IoTime:          stats[9],
			WeightedIoTime:  stats[10],
		}
		diskStatsMap[deviceName] = diskStats
	}
	return diskStatsMap, nil
}

func major(devNumber uint64) uint {
	return uint((devNumber >> 8) & 0xfff)
}

func minor(devNumber uint64) uint {
	return uint((devNumber & 0xff) | ((devNumber >> 12) & 0xfff00))
}

func (self *RealFsInfo) GetDirFsDevice(dir string) (*DeviceInfo, error) {
	buf := new(syscall.Stat_t)
	err := syscall.Stat(dir, buf)
	if err != nil {
		return nil, fmt.Errorf("stat failed on %s with error: %s", dir, err)
	}
	major := major(buf.Dev)
	minor := minor(buf.Dev)
	deviceInfo, err := self.partitionCache.DeviceInfoForMajorMinor(major, minor)
	if err != nil {
		return nil, err
	}
	return deviceInfo, nil
}

func (self *RealFsInfo) GetDirUsage(dir string, timeout time.Duration) (uint64, error) {
	if dir == "" {
		return 0, fmt.Errorf("invalid directory")
	}
	cmd := exec.Command("nice", "-n", "19", "du", "-s", dir)
	stdoutp, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("failed to setup stdout for cmd %v - %v", cmd.Args, err)
	}
	stderrp, err := cmd.StderrPipe()
	if err != nil {
		return 0, fmt.Errorf("failed to setup stderr for cmd %v - %v", cmd.Args, err)
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed to exec du - %v", err)
	}
	stdoutb, souterr := ioutil.ReadAll(stdoutp)
	stderrb, _ := ioutil.ReadAll(stderrp)
	timer := time.AfterFunc(timeout, func() {
		glog.Infof("killing cmd %v due to timeout(%s)", cmd.Args, timeout.String())
		cmd.Process.Kill()
	})
	err = cmd.Wait()
	timer.Stop()
	if err != nil {
		return 0, fmt.Errorf("du command failed on %s with output stdout: %s, stderr: %s - %v", dir, string(stdoutb), string(stderrb), err)
	}
	stdout := string(stdoutb)
	if souterr != nil {
		glog.Errorf("failed to read from stdout for cmd %v - %v", cmd.Args, souterr)
	}
	usageInKb, err := strconv.ParseUint(strings.Fields(stdout)[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse 'du' output %s - %s", stdout, err)
	}
	return usageInKb * 1024, nil
}
