package pod

import (
	"context"
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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"sync"
	"time"
)

type PodSimReconciler struct {
	Client    client.Client
	ClientSet *kubernetes.Clientset
	Scheme    *runtime.Scheme
}

var wg sync.WaitGroup

func (r *PodSimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		Complete(r)
}

func (r *PodSimReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	var (
		ctx = context.Background()
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
			if pod.GetLabels()[nodecontroller.Affinity] != "" ||
				pod.GetLabels()[nodecontroller.AntiAffinity] != "" ||
				pod.GetLabels()[nodecontroller.Exclusion] != "" {
				r.cleanAffinityTags(ctx, pod, nodeName)
			}

			r.releaseResource(ctx, pod, nodeName)

			gracePeriodSeconds := int64(0)
			err = r.ClientSet.CoreV1().Pods(pod.GetNamespace()).Delete(pod.GetName(), &metav1.DeleteOptions{GracePeriodSeconds: &gracePeriodSeconds})
			if err != nil && !apierrors.IsNotFound(err) {
				klog.Errorf("Delete Pod: %v Error: %v", req.String(), err)
			}
			return ctrl.Result{}, nil
		}

		r.SyncFakePod(pod.DeepCopy())

		wg.Add(1)

		go r.SyncGPUPod(ctx, nodeName)

		wg.Wait()
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

func (r *PodSimReconciler) SyncGPUPod(ctx context.Context, nodeName string) {
	time.Sleep(time.Duration(2) * time.Minute)
	scv := &scv1.Scv{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, scv)
	if err != nil {
		klog.Errorf("Node: %v Get Scv Error: %v", nodeName, err)
		return
	}

	podListWithNode := make([]v1.Pod, 0)
	podList := &v1.PodList{}

	err = r.Client.List(ctx, podList)
	if err != nil {
		klog.Errorf("List Pod Error: %v", err)
		return
	}

	for _, pod := range podList.Items {
		if pod.Spec.NodeName == nodeName {
			podListWithNode = append(podListWithNode, pod)
		}
	}

	cardList := scv.Status.CardList

	for i, card := range cardList {
		cardList[i].FreeMemory = card.TotalMemory
	}
	for _, pod := range podListWithNode {
		if pod.GetLabels()[nodecontroller.Affinity] != "" ||
			pod.GetLabels()[nodecontroller.AntiAffinity] != "" ||
			pod.GetLabels()[nodecontroller.Exclusion] != "" {
			r.addAffinityTags(ctx, &pod, nodeName)
			r.scheduleGPUbyKubeShare(pod, cardList, *scv)
		} else {
			r.scheduleGPUbyYoda(pod, cardList, *scv)
		}

		nowTime := metav1.Time{Time: time.Now()}
		pod.SetDeletionTimestamp(&nowTime)  //TODO: 设置pod过一定时间删除

	}

	wg.Done()
}

func (r *PodSimReconciler) scheduleGPUbyYoda(pod v1.Pod, cardList scv1.CardList, scv scv1.Scv) {
	mem, _ := strconv.Atoi(pod.GetLabels()["scv/memory"])
	maxCard := 0
	maxMemory := uint64(0)
	for i, card := range cardList {
		if maxMemory < card.FreeMemory {
			maxCard = i
			maxMemory = card.FreeMemory
		}
	}

	cardList[maxCard].FreeMemory -= uint64(mem)

	freeSum := uint64(0)
	for _, card := range cardList {
		freeSum += card.FreeMemory
	}
	scv.Status.FreeMemorySum = freeSum
	scv.Status.CardList = cardList

	label := map[string]string{
		"scheduleGPUID": strconv.Itoa(maxCard),
	}
	pod.SetLabels(label)

	err := r.Client.Update(context.TODO(), &pod)
	if err != nil {
		klog.Errorf("Pod: %v/%v Update Label Error: %v", pod.GetNamespace(), pod.GetName(), err)
	}

	ops := []util.Ops{
		{
			Op:    "replace",
			Path:  "/status",
			Value: scv.Status,
		},
	}
	err = r.Client.Patch(context.TODO(), &scv, &util.Patch{PatchOps: ops})
	if err != nil {
		klog.Errorf("Scv: %v Patch Status Error: %v", scv.GetName(), err)
	}
}

func (r *PodSimReconciler) scheduleGPUbyKubeShare(pod v1.Pod, cardList scv1.CardList, scv scv1.Scv) {
	mem := StrToUint64(pod.GetLabels()["scv/memory"])
	minSub := ^uint64(0)
	GPUID := 0

	for index, card := range cardList {
		sub := card.FreeMemory - mem
		if sub >= 0 && sub < minSub {
			minSub = sub
			GPUID = index
		}
	}

	label := map[string]string{
		"scheduleGPUID": strconv.Itoa(GPUID),
	}
	pod.SetLabels(label)

	err := r.Client.Update(context.TODO(), &pod)
	if err != nil {
		klog.Errorf("Pod: %v/%v Update Label Error: %v", pod.GetNamespace(), pod.GetName(), err)
	}

	cardList[GPUID].FreeMemory -= mem

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
	err = r.Client.Patch(context.TODO(), &scv, &util.Patch{PatchOps: ops})
	if err != nil {
		klog.Errorf("Scv: %v Patch Status Error: %v", scv.GetName(), err)
	}
}



func (r *PodSimReconciler) addAffinityTags (ctx context.Context, pod *v1.Pod, nodeName string) {
	scv := &scv1.Scv{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, scv)
	if err != nil {
		klog.Errorf("Node: %v Get Scv Error: %v", nodeName, err)
		return
	}

	podListWithNode := make([]v1.Pod, 0)
	podList := &v1.PodList{}

	err = r.Client.List(ctx, podList)
	if err != nil {
		klog.Errorf("List Pod Error: %v", err)
		return
	}

	for _, podItem := range podList.Items {
		if podItem.Spec.NodeName == nodeName {
			podListWithNode = append(podListWithNode, podItem)
		}
	}

	cardList := scv.Status.CardList



	if pod.GetLabels()[nodecontroller.Affinity] != "" {
		for _, podItem := range podListWithNode {
			if pod.GetLabels()[nodecontroller.Affinity] == podItem.GetLabels()[nodecontroller.Affinity] && pod.GetName() != podItem.GetName() {
				return
			}
		}
		GPUID, _ := strconv.Atoi(pod.GetLabels()["scheduleGPUID"])
		cardList[GPUID].AffinityTag = append(cardList[GPUID].AffinityTag, pod.GetLabels()[nodecontroller.Affinity])
	}

	if pod.GetLabels()[nodecontroller.AntiAffinity] != "" {
		for _, podItem := range podListWithNode {
			if pod.GetLabels()[nodecontroller.AntiAffinity] == podItem.GetLabels()[nodecontroller.AntiAffinity] && pod.GetName() != podItem.GetName() {
				return
			}
		}
		GPUID, _ := strconv.Atoi(pod.GetLabels()["scheduleGPUID"])
		cardList[GPUID].AntiAffinityTag = append(cardList[GPUID].AntiAffinityTag, pod.GetLabels()[nodecontroller.AntiAffinity])
	}

	if pod.GetLabels()[nodecontroller.Exclusion] != "" {
		for _, podItem := range podListWithNode {
			if pod.GetLabels()[nodecontroller.Exclusion] == podItem.GetLabels()[nodecontroller.Exclusion] && pod.GetName() != podItem.GetName() {
				return
			}
		}
		GPUID, _ := strconv.Atoi(pod.GetLabels()["scheduleGPUID"])
		cardList[GPUID].ExclusionTag = append(cardList[GPUID].ExclusionTag, pod.GetLabels()[nodecontroller.Exclusion])
	}
}

func (r *PodSimReconciler) cleanAffinityTags (ctx context.Context, pod *v1.Pod, nodeName string) {
	scv := &scv1.Scv{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, scv)
	if err != nil {
		klog.Errorf("Node: %v Get Scv Error: %v", nodeName, err)
		return
	}

	podListWithNode := make([]v1.Pod, 0)
	podList := &v1.PodList{}

	err = r.Client.List(ctx, podList)
	if err != nil {
		klog.Errorf("List Pod Error: %v", err)
		return
	}

	for _, podItem := range podList.Items {
		if podItem.Spec.NodeName == nodeName {
			podListWithNode = append(podListWithNode, podItem)
		}
	}

	cardList := scv.Status.CardList



	if pod.GetLabels()[nodecontroller.Affinity] != "" {
		for _, podItem := range podListWithNode {
			if pod.GetLabels()[nodecontroller.Affinity] == podItem.GetLabels()[nodecontroller.Affinity] && pod.GetName() != podItem.GetName() {
				return
			}
		}
		GPUID, _ := strconv.Atoi(pod.GetLabels()["scheduleGPUID"])
		RemoveParam(cardList[GPUID].AffinityTag, pod.GetLabels()[nodecontroller.Affinity])
	}

	if pod.GetLabels()[nodecontroller.AntiAffinity] != "" {
		for _, podItem := range podListWithNode {
			if pod.GetLabels()[nodecontroller.AntiAffinity] == podItem.GetLabels()[nodecontroller.AntiAffinity] && pod.GetName() != podItem.GetName() {
				return
			}
		}
		GPUID, _ := strconv.Atoi(pod.GetLabels()["scheduleGPUID"])
		RemoveParam(cardList[GPUID].AntiAffinityTag, pod.GetLabels()[nodecontroller.AntiAffinity])
	}

	if pod.GetLabels()[nodecontroller.Exclusion] != "" {
		for _, podItem := range podListWithNode {
			if pod.GetLabels()[nodecontroller.Exclusion] == podItem.GetLabels()[nodecontroller.Exclusion] && pod.GetName() != podItem.GetName() {
				return
			}
		}
		GPUID, _ := strconv.Atoi(pod.GetLabels()["scheduleGPUID"])
		RemoveParam(cardList[GPUID].ExclusionTag, pod.GetLabels()[nodecontroller.Exclusion])
	}
}

func (r *PodSimReconciler) releaseResource (ctx context.Context, pod *v1.Pod, nodeName string) {
	scv := &scv1.Scv{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: nodeName}, scv)
	if err != nil {
		klog.Errorf("Node: %v Get Scv Error: %v", nodeName, err)
		return
	}

	cardList := scv.Status.CardList

	GPUID, _ := strconv.Atoi(pod.GetLabels()["scheduleGPUID"])
	mem := StrToUint64(pod.GetLabels()["scv/memory"])
	cardList[GPUID].FreeMemory += mem

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

func RemoveParam(sli []string, n string) []string {
	for i := 0; i < len(sli); i++ {
		if sli[i] == n {
			if i == 0 {
				sli = sli[1:]
			} else if i == len(sli) - 1 {
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