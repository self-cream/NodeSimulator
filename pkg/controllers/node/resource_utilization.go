package node

import (
	"context"
	"errors"
	"fmt"

	scv "github.com/NJUPT-ISL/SCV/api/v1"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	v1 "k8s.io/api/core/v1"

	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"time"
)

// NodeSimulatorReconciler reconciles a NodeSimulator object
type ResourceUtilizationUpdater struct {
	Client   client.Client
	Queue    workqueue.RateLimitingInterface
	StopChan chan struct{}
}

func NewResourceUtilizationUpdater(updaterClient client.Client, queue workqueue.RateLimitingInterface, stopChan chan struct{}) (*ResourceUtilizationUpdater, error) {
	if updaterClient == nil || queue == nil || stopChan == nil {
		return nil, errors.New("New ResourceUtilizationUpdater Error, parameters contains nil ")
	}
	return &ResourceUtilizationUpdater{
		Client:   updaterClient,
		Queue:    queue,
		StopChan: stopChan,
	}, nil
}

func (n *ResourceUtilizationUpdater) processNextItem() bool {

	ctx := context.TODO()
	// Wait until there is a new item in the working queue
	key, quit := n.Queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two pods with the same key are never processed in
	// parallel.
	defer n.Queue.Done(key)

	if node, ok := key.(*v1.Node); ok {
		nodeName := node.GetName()
		cardPercentage, nodePercentage := n.SyncResourceUtilization(ctx, node)
		for index, perCardPercent := range cardPercentage {
			fmt.Printf("node name: %s, card ID: %d, utilization percent: %f\n", nodeName, index, perCardPercent)
		}
		fmt.Printf("----node name: %s, node utilization percent: %f\n", nodeName, nodePercentage)
	} else {
		klog.Errorf("Key in Queue is not Node Type. ")
	}
	// Invoke the method containing the business logic
	return true
}

func (n *ResourceUtilizationUpdater) runWorker() {
	for n.processNextItem() {
	}
}

func (n *ResourceUtilizationUpdater) InitUpdater() {
	for {
		time.Sleep(30 * time.Second)
		fmt.Printf("----Update start----\n")
		nodeList := &v1.NodeList{}
		err := n.Client.List(context.TODO(), nodeList)
		if err != nil {
			klog.Errorf("List Node Error: %v", err)
			continue
		}
		if nodeList.Items != nil && len(nodeList.Items) > 0 {
			for _, node := range nodeList.Items {
				labels := node.GetLabels()
				if labels != nil {
					if v, ok := labels[ManageLabelKey]; ok && v == ManageLabelValue {
						n.Queue.Add(node.DeepCopy())
					}
				}
			}
		}
	}
}

func (n *ResourceUtilizationUpdater) Run(threadiness int, stopCh chan struct{}) {
	defer runtime.HandleCrash()

	// Let the workers stop when we are done
	defer n.Queue.ShutDown()
	klog.Info("Starting resource utilization updater")

	go n.InitUpdater()

	for i := 0; i < threadiness; i++ {
		go wait.Until(n.runWorker, time.Second, stopCh)
	}

	<-stopCh
	klog.Info("Stopping resource utilization updater")
}

func (n *ResourceUtilizationUpdater) SyncResourceUtilization(ctx context.Context, node *v1.Node) ([]float64, float64) {
	currentScv := &scv.Scv{}
	nodeName := node.GetName()

	err := n.Client.Get(ctx, types.NamespacedName{Name: nodeName}, currentScv)
	if err != nil {
		klog.Errorf("Node: %v Get Scv Error: %v", nodeName, err)
	}

	cardList := currentScv.Status.CardList

	//define card and node GPU memory utilization
	cardPercentage := make([]float64, len(cardList))
	nodePercentage := float64(0)

	for index, card := range cardList {
		cardPercentage[index] = 1 - (float64(card.FreeMemory) / float64(card.TotalMemory))
	}

	nodePercentage = 1 - (float64(currentScv.Status.FreeMemorySum) / float64(currentScv.Status.TotalMemorySum))

	return cardPercentage, nodePercentage
}
