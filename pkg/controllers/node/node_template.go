package node

import (
	simv1 "github.com/NJUPT-ISL/NodeSimulator/pkg/api/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
	"strconv"
)

func SelectGPUModel(nodesim *simv1.NodeSimulator) *simv1.NodeSimulator {
	if nodesim.Spec.GpuModel == "GTX-1660" {
		nodesim.Spec.GPU.Memory = strconv.Itoa(6000)
		nodesim.Spec.GPU.Core = strconv.Itoa(1785)
		nodesim.Spec.GPU.Bandwidth = strconv.Itoa(188)
		nodesim.Spec.GPU.CoreNumber = 1408
	}

	if nodesim.Spec.GpuModel == "TITAN-Xp" {
		nodesim.Spec.GPU.Memory = strconv.Itoa(12288)
		nodesim.Spec.GPU.Core = strconv.Itoa(1582)
		nodesim.Spec.GPU.Bandwidth = strconv.Itoa(548)
		nodesim.Spec.GPU.CoreNumber = 3840
	}

	if nodesim.Spec.GpuModel == "Tesla P100" {
		nodesim.Spec.GPU.Memory = strconv.Itoa(16384)
		nodesim.Spec.GPU.Core = strconv.Itoa(1328)
		nodesim.Spec.GPU.Bandwidth = strconv.Itoa(720)
		nodesim.Spec.GPU.CoreNumber = 3584
	}

	return nodesim
}

func GenNode(nodesim *simv1.NodeSimulator) (*v1.Node, error) {
	labels := make(map[string]string, 0)
	labels[ManageLabelKey] = ManageLabelValue
	labels[UniqueLabelKey] = nodesim.GetNamespace() + "-" + nodesim.GetName()
	labels[RegionLabelKey] = nodesim.Spec.Region
	podcidr := make([]string, 0)
	podcidr = append(podcidr, nodesim.Spec.PodCidr)
	cpu, err := resource.ParseQuantity(nodesim.Spec.Cpu)
	if err != nil {
		klog.Errorf("NodeSim: %v/%v CPU ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
		return nil, err
	}

	memory, err := resource.ParseQuantity(nodesim.Spec.Memory)
	if err != nil {
		klog.Errorf("NodeSim: %v/%v Memory ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
		return nil, err
	}
	pods, err := resource.ParseQuantity(nodesim.Spec.PodNumber)
	if err != nil {
		klog.Errorf("NodeSim: %v/%v Pods ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
		return nil, err
	}

	disk, err := resource.ParseQuantity(nodesim.Spec.Disk)
	if err != nil {
		klog.Errorf("NodeSim: %v/%v Disk ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
		return nil, err
	}

	bandwidth, err := resource.ParseQuantity(nodesim.Spec.Bandwidth)
	if err != nil {
		klog.Errorf("NodeSim: %v/%v Bandwidth ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
		return nil, err
	}

	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: v1.NodeSpec{
			PodCIDR:  nodesim.Spec.PodCidr,
			PodCIDRs: podcidr,
		},
		Status: v1.NodeStatus{
			Capacity: map[v1.ResourceName]resource.Quantity{
				"cpu":       cpu,
				"memory":    memory,
				"pods":      pods,
				"disk":      disk,
				"bandwidth": bandwidth,
			},
			Allocatable: map[v1.ResourceName]resource.Quantity{
				"cpu":       cpu,
				"memory":    memory,
				"pods":      pods,
				"disk":      disk,
				"bandwidth": bandwidth,
			},

			NodeInfo: v1.NodeSystemInfo{
				OperatingSystem:         NodeOS,
				Architecture:            NodeArch,
				OSImage:                 NodeOSImage,
				KernelVersion:           NodeKernel,
				KubeletVersion:          NodeKubeletVersion,
				KubeProxyVersion:        NodeKubeletVersion,
				ContainerRuntimeVersion: NodeDockerVersion,
			},
		},
	}

	if nodesim.Spec.GpuModel != "" {
		nodesim = SelectGPUModel(nodesim)

		if nodesim.Spec.GPU.Number > 0 {
			number, err := resource.ParseQuantity(strconv.Itoa(nodesim.Spec.GPU.Number))
			if err != nil {
				klog.Errorf("NodeSim: %v/%v GPU Number ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
				return nil, err
			}
			node.Status.Allocatable["gpu/number"] = number
			node.Status.Capacity["gpu/number"] = number

			bandwidth, err := resource.ParseQuantity(nodesim.Spec.GPU.Bandwidth)
			if err != nil {
				klog.Errorf("NodeSim: %v/%v GPU Bandwidth ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
				return nil, err
			}
			node.Status.Allocatable["gpu/bandwidth"] = bandwidth
			node.Status.Capacity["gpu/bandwidth"] = bandwidth

			mem, _ := strconv.Atoi(nodesim.Spec.GPU.Memory)
			memory, err := resource.ParseQuantity(strconv.Itoa(mem * nodesim.Spec.GPU.Number))
			if err != nil {
				klog.Errorf("NodeSim: %v/%v GPU Memory ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
				return nil, err
			}
			node.Status.Allocatable["gpu/memory"] = memory
			node.Status.Capacity["gpu/memory"] = memory

			core, err := resource.ParseQuantity(nodesim.Spec.GPU.Core)
			if err != nil {
				klog.Errorf("NodeSim: %v/%v GPU Core ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
				return nil, err
			}
			node.Status.Allocatable["gpu/core"] = core
			node.Status.Capacity["gpu/core"] = core

			coreNumber, err := resource.ParseQuantity(strconv.Itoa(nodesim.Spec.GPU.CoreNumber))
			if err != nil {
				klog.Errorf("NodeSim: %v/%v GPU Core number ParseQuantity Error: %v", nodesim.GetNamespace(), nodesim.GetName(), err)
				return nil, err
			}
			node.Status.Allocatable["gpu/coreNumber"] = coreNumber
			node.Status.Capacity["gpu/coreNumber"] = coreNumber

		}
	}

	return node, nil
}
