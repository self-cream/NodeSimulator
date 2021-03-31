package pod

import (
	"context"
	"fmt"
	nodecontroller "github.com/NJUPT-ISL/NodeSimulator/pkg/controllers/node"
	"github.com/NJUPT-ISL/NodeSimulator/pkg/util"
	scv1 "github.com/NJUPT-ISL/SCV/api/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	"math/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"time"
)

type PodSimReconciler struct {
	Client    client.Client
	ClientSet *kubernetes.Clientset
	Scheme    *runtime.Scheme
}

func (r *PodSimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		Complete(r)
}

func (r *PodSimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		pod = &v1.Pod{}
		err = r.Client.Get(ctx, req.NamespacedName, pod)
	)

	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.Warningf("PodSim: %v Not Found. ", req.NamespacedName.String())
		} else {
			klog.Errorf("PodSim: %v Error: %v ", req.NamespacedName.String(), err)
		}
		return ctrl.Result{}, nil
	}

	labels := pod.GetLabels()
	if labels == nil {
		return ctrl.Result{}, nil
	}
	if v, ok := labels[nodecontroller.ManageLabelKey]; ok && v == nodecontroller.ManageLabelValue {
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			return ctrl.Result{}, nil
		}

		if pod.GetDeletionTimestamp() != nil {
			scv := &scv1.Scv{}
			err := r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, scv)
			if err != nil {
				klog.Errorf("Node: %v Get Scv Error: %v", nodeName, err)
				return ctrl.Result{}, err
			}

			podListWithNode := make([]v1.Pod, 0)
			podList := &v1.PodList{}

			err = r.Client.List(ctx, podList, &client.MatchingLabels{
				nodecontroller.ManageLabelKey: nodecontroller.ManageLabelValue,
			})
			if err != nil {
				klog.Errorf("List Pod Error: %v", err)
				return ctrl.Result{}, err
			}

			for _, pod := range podList.Items {
				if pod.Spec.NodeName == nodeName {
					if pod.GetDeletionTimestamp() == nil {
						podListWithNode = append(podListWithNode, pod)
					}
				}
			}

			cardList := scv.Status.CardList

			for i, card := range cardList {
				cardList[i].FreeMemory = card.TotalMemory
			}

			for _, pod := range podListWithNode {
				labels := pod.GetLabels()
				mem := StrToUint64(labels["scv/memory"])
				//minSub := ^uint64(0)
				GPUID := 0

				//for index, card := range cardList {
				//	sub := card.FreeMemory - mem
				//	if sub >= 0 && sub < minSub {
				//		minSub = sub
				//		GPUID = index
				//	}
				//}
				if gpuID, ok := labels[scheduleGPUID]; ok {
					GPUID, err = strconv.Atoi(gpuID)
					if err != nil {
						cardList[GPUID].FreeMemory -= mem
					}
				}
			}

			freeSum := uint64(0)
			for _, card := range cardList {
				freeSum += card.FreeMemory
			}

			scv.Status.FreeMemorySum = freeSum
			scv.Status.CardList = cardList

			ops := []util.Ops{
				{
					Op:    "replace",
					Path:  "/status",
					Value: scv.Status,
				},
			}

			err = r.Client.Patch(context.TODO(), scv, &util.Patch{PatchOps: ops})

			if err != nil {
				klog.Errorf("Scv: %v Patch Status Error: %v", scv.GetName(), err)
			}
			_, hasAffinity := labels[nodecontroller.Affinity]
			_, hasAntiAffinity := labels[nodecontroller.AntiAffinity]
			_, hasExclusion := labels[nodecontroller.Exclusion]
			if hasAffinity || hasAntiAffinity || hasExclusion {
				r.cleanAffinityTags(labels, ctx, pod, nodeName)
			}

			gracePeriodSeconds := int64(0)
			err = r.ClientSet.CoreV1().Pods(pod.GetNamespace()).Delete(context.TODO(), pod.GetName(), metav1.DeleteOptions{GracePeriodSeconds: &gracePeriodSeconds})

			if err != nil && !apierrors.IsNotFound(err) {
				klog.Errorf("Delete Pod: %v Error: %v", req.String(), err)
			}
			return ctrl.Result{}, nil
		}
		r.SyncFakePod(pod.DeepCopy())
		r.SyncGPUPod(ctx, *pod)
	}

	return ctrl.Result{}, nil
}

func (r *PodSimReconciler) SyncFakePod(pod *v1.Pod) {
	updateTime := metav1.Time{Time: time.Now()}
	containerStatusList := make([]v1.ContainerStatus, 0)
	for _, container := range pod.Spec.Containers {
		runningState := &v1.ContainerStateRunning{
			StartedAt: updateTime,
		}
		started := true
		containerStatus := v1.ContainerStatus{
			Name: container.Name,
			State: v1.ContainerState{
				Running: runningState,
			},
			Ready:        true,
			Image:        container.Image,
			Started:      &started,
			RestartCount: 0,
			ImageID:      "docker://sim.k8s.io/podSim/image/" + container.Image,
		}
		containerStatusList = append(containerStatusList, containerStatus)
	}
	conditions := []v1.PodCondition{
		{
			LastProbeTime:      updateTime,
			LastTransitionTime: updateTime,
			Status:             v1.ConditionTrue,
			Type:               v1.PodInitialized,
		},
		{
			LastProbeTime:      updateTime,
			LastTransitionTime: updateTime,
			Status:             v1.ConditionTrue,
			Type:               v1.PodReady,
		},
		{
			LastProbeTime:      updateTime,
			LastTransitionTime: updateTime,
			Status:             v1.ConditionTrue,
			Type:               v1.ContainersReady,
		},
		{
			LastProbeTime:      updateTime,
			LastTransitionTime: updateTime,
			Status:             v1.ConditionTrue,
			Type:               v1.PodScheduled,
		},
	}

	podStatus := v1.PodStatus{
		HostIP:            "10.0.0.1",
		Phase:             v1.PodRunning,
		PodIP:             "10.224.0.1",
		QOSClass:          v1.PodQOSBurstable,
		StartTime:         &updateTime,
		Conditions:        conditions,
		ContainerStatuses: containerStatusList,
	}

	ops := []util.Ops{
		{
			Op:    "replace",
			Path:  "/status",
			Value: podStatus,
		},
	}
	err := r.Client.Status().Patch(context.TODO(), pod, &util.Patch{PatchOps: ops})
	if err != nil {
		klog.Errorf("Pod: %v/%v Patch Status Error: %v", pod.GetNamespace(), pod.GetName(), err)
	}
} //TODO: CPU,memory的allocatable数值的更新

func (r *PodSimReconciler) SyncGPUPod(ctx context.Context, pod v1.Pod) {
	scv := &scv1.Scv{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, scv)
	if err != nil {
		klog.Errorf("Node: %v Get Scv Error: %v", pod.Spec.NodeName, err)
		return
	}

	podListWithNode := make([]v1.Pod, 0)
	podList := &v1.PodList{}

	err = r.Client.List(ctx, podList, &client.MatchingLabels{
		nodecontroller.ManageLabelKey: nodecontroller.ManageLabelValue,
	})
	if err != nil {
		klog.Errorf("List Pod Error: %v", err)
		return
	}

	for _, item := range podList.Items {
		if item.Spec.NodeName == pod.Spec.NodeName {
			podListWithNode = append(podListWithNode, item)
		}
	}

	cardList := scv.Status.CardList

	labels := pod.GetLabels()
	_, ok := labels[scheduleGPUID]

	if len(podListWithNode) == 1 && !ok {
		fmt.Println("start to initialize cardlist")
		for i, card := range cardList {
			cardList[i].FreeMemory = card.TotalMemory
		}

		freeSum := uint64(0)
		for _, card := range cardList {
			freeSum += card.FreeMemory
		}

		scv.Status.FreeMemorySum = freeSum
		scv.Status.CardList = cardList

		ops := []util.Ops{
			{
				Op:    "replace",
				Path:  "/status",
				Value: scv.Status,
			},
		}

		err = r.Client.Patch(context.TODO(), scv, &util.Patch{PatchOps: ops})
		if err != nil {
			klog.Errorf("Scv: %v Patch Status Error: %v", scv.GetName(), err)
		}
	}

	for _, pod := range podListWithNode {
		labels := pod.GetLabels()
		_, hasAffinity := labels[nodecontroller.Affinity]
		antiAffinityTag, hasAntiAffinity := labels[nodecontroller.AntiAffinity]
		exclusionTag, hasExclusion := labels[nodecontroller.Exclusion]
		if hasAffinity || hasAntiAffinity || hasExclusion {
			mem := StrToUint64(labels["scv/memory"])
			minSub := uint64(0)
			GPUID := 0
			filterCardList := cardList.DeepCopy()
			if hasAntiAffinity {
				for _, card := range filterCardList {
					for _, tag := range card.AntiAffinityTag {
						if antiAffinityTag == tag {
							filterCardList = RemoveCard(filterCardList, card)
							break
						}
					}
				}
			}

			if hasExclusion {
				for _, card := range filterCardList {
					if len(card.ExclusionTag) != 0 {
						for _, tag := range card.ExclusionTag {
							if exclusionTag != tag {
								filterCardList = RemoveCard(filterCardList, card)
								break
							}
						}
					}
				}
			}

			for _, card := range filterCardList {
				sub := card.FreeMemory - mem
				if sub >= 0 && sub > minSub {
					minSub = sub
					GPUID = int(card.ID)
				}
			}

			if _, ok := labels[scheduleGPUID]; !ok {
				fmt.Println("start to set GPUID label")
				labels[scheduleGPUID] = strconv.Itoa(GPUID)
				updatePod := pod.DeepCopy()
				updatePod.SetLabels(labels)

				ops := []util.Ops{
					{
						Op:    "replace",
						Path:  "/metadata/labels",
						Value: updatePod.GetLabels(),
					},
				}

				err = r.Client.Patch(context.TODO(), updatePod, &util.Patch{PatchOps: ops})

				if err != nil {
					klog.Errorf("Pod: %v/%v Patch Label Error: %v", updatePod.GetNamespace(), updatePod.GetName(), err)
				}

				cardList[GPUID].FreeMemory -= mem
			}
		} else if pod.Spec.SchedulerName == "native-scheduler" {
			if _, ok := labels[scheduleGPUID]; !ok {
				mem := int64(0)
				for _, container := range pod.Spec.Containers {
					quantity := container.Resources.Requests["gpu/memory"]
					q := quantity.DeepCopy()
					mem += q.Value()
				}
				rand.Seed(time.Now().UnixNano())
				cardID := rand.Intn(4)
				fmt.Println("start to set GPUID label--native scheduler")
				labels[scheduleGPUID] = strconv.Itoa(cardID)
				updatePod := pod.DeepCopy()
				updatePod.SetLabels(labels)

				ops := []util.Ops{
					{
						Op:    "replace",
						Path:  "/metadata/labels",
						Value: updatePod.GetLabels(),
					},
				}

				err = r.Client.Patch(context.TODO(), updatePod, &util.Patch{PatchOps: ops})

				if err != nil {
					klog.Errorf("Pod: %v/%v Patch Label Error: %v", updatePod.GetNamespace(), updatePod.GetName(), err)
				}

				cardList[cardID].FreeMemory -= uint64(mem)
			}
		} else {
			mem, _ := strconv.Atoi(labels["scv/memory"])
			maxCard := 0
			maxMemory := uint64(0)
			for i, card := range cardList {
				if maxMemory < card.FreeMemory {
					maxCard = i
					maxMemory = card.FreeMemory
				}
			}

			if _, ok := labels[scheduleGPUID]; !ok {
				fmt.Println("start to set GPUID label")
				labels[scheduleGPUID] = strconv.Itoa(maxCard)
				updatePod := pod.DeepCopy()
				updatePod.SetLabels(labels)

				ops := []util.Ops{
					{
						Op:    "replace",
						Path:  "/metadata/labels",
						Value: updatePod.GetLabels(),
					},
				}

				err = r.Client.Patch(context.TODO(), updatePod, &util.Patch{PatchOps: ops})

				if err != nil {
					klog.Errorf("Pod: %v/%v Patch Label Error: %v", updatePod.GetNamespace(), updatePod.GetName(), err)
				}

				cardList[maxCard].FreeMemory -= uint64(mem)
			}
		}
	}

	freeSum := uint64(0)
	for _, card := range cardList {
		freeSum += card.FreeMemory
	}

	scv.Status.FreeMemorySum = freeSum
	scv.Status.CardList = cardList

	ops := []util.Ops{
		{
			Op:    "replace",
			Path:  "/status",
			Value: scv.Status,
		},
	}

	time.Sleep(10 * time.Second)

	err = r.Client.Patch(context.TODO(), scv, &util.Patch{PatchOps: ops})
	if err != nil {
		klog.Errorf("Scv: %v Patch Status Error: %v", scv.GetName(), err)
	}

	for _, pod := range podListWithNode {
		labels := pod.GetLabels()
		isFound := false

		if value, ok := labels[nodecontroller.Affinity]; ok {
			if id, ok := labels[scheduleGPUID]; ok {
				GPUID, err := strconv.Atoi(id)
				if err != nil {
					fmt.Println("convert GPUID failed")
				}
				for _, tag := range cardList[GPUID].AffinityTag {
					if value == tag {
						isFound = true
					}
				}
			}

			if !isFound {
				if id, ok := labels[scheduleGPUID]; ok {
					GPUID, err := strconv.Atoi(id)
					if err != nil {
						fmt.Println("convert GPUID failed")
					}
					cardList[GPUID].AffinityTag = append(cardList[GPUID].AffinityTag, value)
					fmt.Println(cardList[GPUID].AffinityTag)
				}

			}
		}

		if value, ok := labels[nodecontroller.AntiAffinity]; ok {
			isFound = false
			if id, ok := labels[scheduleGPUID]; ok {
				GPUID, err := strconv.Atoi(id)
				if err != nil {
					fmt.Println("convert GPUID failed")
				}
				for _, tag := range cardList[GPUID].AntiAffinityTag {
					if value == tag {
						isFound = true
					}
				}
			}
			if !isFound {
				if id, ok := labels[scheduleGPUID]; ok {
					GPUID, err := strconv.Atoi(id)
					if err != nil {
						fmt.Println("convert GPUID failed")
					}
					cardList[GPUID].AntiAffinityTag = append(cardList[GPUID].AntiAffinityTag, value)
				}

			}
		}

		if value, ok := labels[nodecontroller.Exclusion]; ok {
			isFound = false
			if id, ok := labels[scheduleGPUID]; ok {
				GPUID, _ := strconv.Atoi(id)
				if len(cardList[GPUID].ExclusionTag) == 0 {
					cardList[GPUID].ExclusionTag = append(cardList[GPUID].ExclusionTag, value)
				} else {
					for _, tag := range cardList[GPUID].ExclusionTag {
						if value != tag {
							fmt.Println("Pod exclusion tag mismatch with node")
						}
					}
				}
			}
		}

		scv.Status.CardList = cardList

		ops := []util.Ops{
			{
				Op:    "replace",
				Path:  "/status",
				Value: scv.Status,
			},
		}
		err = r.Client.Patch(context.TODO(), scv, &util.Patch{PatchOps: ops})
		if err != nil {
			klog.Errorf("Scv: %v Patch Status Error: %v", scv.GetName(), err)
		}
	}
}

func (r *PodSimReconciler) cleanAffinityTags(labels map[string]string, ctx context.Context, pod *v1.Pod, nodeName string) {
	fmt.Println("ready to clean affinity tags")
	scv := &scv1.Scv{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, scv)
	if err != nil {
		klog.Errorf("Node: %v Get Scv Error: %v", nodeName, err)
		return
	}

	cardList := scv.Status.CardList

	podListWithGPU := make([]v1.Pod, 0)
	podList := &v1.PodList{}

	err = r.Client.List(ctx, podList, &client.MatchingLabels{
		nodecontroller.ManageLabelKey: nodecontroller.ManageLabelValue,
	})
	if err != nil {
		klog.Errorf("List Pod Error: %v", err)
		return
	}

	for _, podItem := range podList.Items {
		if pod.Spec.NodeName == podItem.Spec.NodeName && pod.GetName() != podItem.GetName() {
			existLabels := podItem.GetLabels()
			if existLabels[scheduleGPUID] == labels[scheduleGPUID] {
				podListWithGPU = append(podListWithGPU, podItem)
			}
		}
	}

	fmt.Println("get pod list success")

	alreadyExist := false
	if value, ok := labels[nodecontroller.Affinity]; ok {
		fmt.Println("get pod affinity success")
		for _, podItem := range podListWithGPU {
			existLabels := podItem.GetLabels()
			if v, ok := existLabels[nodecontroller.Affinity]; ok {
				fmt.Println("get  existing pod affinity success")
				if value == v && pod.GetName() != podItem.GetName() {
					alreadyExist = true
				}
			}
		}
		if !alreadyExist {
			fmt.Println("start to delete affinity tag")
			if gpuID, ok := labels[scheduleGPUID]; ok {
				GPUID, err := strconv.Atoi(gpuID)
				if err != nil {
					fmt.Println("convert failed")
				}
				cardList[GPUID].AffinityTag = RemoveParam(cardList[GPUID].AffinityTag, value)
				fmt.Println(cardList[GPUID].AffinityTag)
			}
		}
	}

	if value, ok := labels[nodecontroller.AntiAffinity]; ok {
		alreadyExist = false
		for _, podItem := range podListWithGPU {
			existLabels := podItem.GetLabels()
			if v, ok := existLabels[nodecontroller.AntiAffinity]; ok {
				if value == v && pod.GetName() != podItem.GetName() {
					alreadyExist = true
				}
			}
		}
		if !alreadyExist {
			if gpuID, ok := labels[scheduleGPUID]; ok {
				GPUID, err := strconv.Atoi(gpuID)
				if err != nil {
					fmt.Println("convert failed")
				}
				cardList[GPUID].AntiAffinityTag = RemoveParam(cardList[GPUID].AntiAffinityTag, value)
			}
		}
	}

	if value, ok := labels[nodecontroller.Exclusion]; ok {
		alreadyExist = false
		for _, podItem := range podListWithGPU {
			existLabels := podItem.GetLabels()
			if v, ok := existLabels[nodecontroller.Exclusion]; ok {
				if value == v && pod.GetName() != podItem.GetName() {
					alreadyExist = true
				}
			}
		}
		if !alreadyExist {
			if gpuID, ok := labels[scheduleGPUID]; ok {
				GPUID, err := strconv.Atoi(gpuID)
				if err != nil {
					fmt.Println("convert failed")
				}
				cardList[GPUID].ExclusionTag = RemoveParam(cardList[GPUID].ExclusionTag, value)
			}
		}
	}

	scv.Status.CardList = cardList

	ops := []util.Ops{
		{
			Op:    "replace",
			Path:  "/status",
			Value: scv.Status,
		},
	}

	err = r.Client.Patch(context.TODO(), scv, &util.Patch{PatchOps: ops})
	if err != nil {
		klog.Errorf("Scv: %v Patch Status Error: %v", scv.GetName(), err)
	}
}

func RemoveParam(sli []string, n string) []string {
	for i := 0; i < len(sli); i++ {
		if sli[i] == n {
			if i == 0 {
				sli = sli[1:]
			} else if i == len(sli)-1 {
				sli = sli[:i]
			} else {
				sli = append(sli[:i], sli[i+1:]...)
			}
			i--
		}
	}
	return sli
}

func StrToUint64(str string) uint64 {
	if i, e := strconv.Atoi(str); e != nil {
		return 0
	} else {
		return uint64(i)
	}
}

func RemoveCard(sli []scv1.Card, n scv1.Card) []scv1.Card {
	for i := 0; i < len(sli); i++ {
		if sli[i].ID == n.ID {
			if i == 0 {
				sli = sli[1:]
			} else if i == len(sli)-1 {
				sli = sli[:i]
			} else {
				sli = append(sli[:i], sli[i+1:]...)
			}
			i--
		}
	}
	return sli
}
