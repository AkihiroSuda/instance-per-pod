package gke

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	containerv1 "cloud.google.com/go/container/apiv1"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	containerv1pb "google.golang.org/genproto/googleapis/container/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/kubernetes"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/AkihiroSuda/instance-per-pod/pkg/jsonpatch"
)

// New instantiates GKE driver.
// parent is used by GKE API, in the format 'projects/*/locations/*/clusters/*'.
func New(ctx context.Context, clientSet *kubernetes.Clientset, parent string) (*Driver, error) {
	var err error
	d := &Driver{
		nodeClient: clientSet.CoreV1().Nodes(),
		parent:     parent,
	}
	d.cmc, err = containerv1.NewClusterManagerClient(ctx)
	if err != nil {
		return nil, err
	}
	return d, nil
}

type Driver struct {
	nodeClient   clientcorev1.NodeInterface
	parent       string
	ippNodePools []string
	cmc          *containerv1.ClusterManagerClient
	cmcMu        sync.Mutex
	extendNodeMu sync.Mutex
}

const (
	nodePoolMetadataIPPReserved = "ipp-reserved"
)

func (d *Driver) getIPPNodePools(ctx context.Context) ([]*containerv1pb.NodePool, error) {
	d.cmcMu.Lock()
	defer d.cmcMu.Unlock()
	glog.Infof("getting node pool for parent %q", d.parent)
	req := &containerv1pb.ListNodePoolsRequest{
		Parent: d.parent,
	}
	resp, err := d.cmc.ListNodePools(ctx, req)
	if err != nil {
		return nil, err
	}
	var pools []*containerv1pb.NodePool
nodePoolLoop:
	for _, p := range resp.NodePools {
		if p.Config == nil {
			continue
		}
		for k, v := range p.Config.Metadata {
			if k == nodePoolMetadataIPPReserved && v == "true" {
				glog.Infof("Found GKE node pool with \"ipp-reserved\": %q", p.Name)
				pools = append(pools, p)
				continue nodePoolLoop
			}
		}
	}
	return pools, nil
}

func (d *Driver) Run(ctx context.Context) error {
	pools, err := d.getIPPNodePools(ctx)
	if err != nil {
		return err
	}
	for _, p := range pools {
		d.ippNodePools = append(d.ippNodePools, p.Name)
	}
	for {
		// TODO: execute increaseNodePool routine here using queue
		runtime.Gosched()
	}
}

func (d *Driver) chooseNodePool(ctx context.Context, pod *corev1.Pod) (string, error) {
	if len(d.ippNodePools) != 1 {
		glog.Infof("currently only single node pool is supported, has %d pools", len(d.ippNodePools))
	}
	return d.ippNodePools[0], nil
}

func (d *Driver) getNodesInPool(ctx context.Context, nodePool string) ([]string, error) {
	labelSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{"cloud.google.com/gke-nodepool": nodePool},
	}
	sel, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		return nil, err
	}
	opts := metav1.ListOptions{
		LabelSelector: sel.String(),
	}
	nodes, err := d.nodeClient.List(opts)
	if err != nil {
		return nil, err
	}
	var nodeNames []string
	for _, item := range nodes.Items {
		nodeNames = append(nodeNames, item.Name)
	}
	return nodeNames, nil
}

func (d *Driver) resizeNodePoolOperation(ctx context.Context, nodePool string, nodeCount uint) (*containerv1pb.Operation, error) {
	d.cmcMu.Lock()
	defer d.cmcMu.Unlock()
	name := d.parent + "/nodePools/" + nodePool
	req := &containerv1pb.SetNodePoolSizeRequest{
		Name:      name,
		NodeCount: int32(nodeCount),
	}
	return d.cmc.SetNodePoolSize(ctx, req)
}

type GKENameObject struct {
	Projects   string
	Locations  string
	Clusters   string
	Operations string
}

func ParseGKENameObject(name string) (*GKENameObject, error) {
	o := &GKENameObject{}
	ss := strings.Split(name, "/")
	currentKey := ""
	for _, s := range ss {
		if s == "projects" || s == "locations" || s == "clusters" || s == "operations" {
			currentKey = s
			continue
		}
		switch currentKey {
		case "projects":
			o.Projects = s
		case "locations":
			o.Locations = s
		case "clusters":
			o.Clusters = s
		case "operations":
			o.Operations = s
		}
	}
	return o, nil
}

func (d *Driver) waitOperationCompletion(ctx context.Context, op *containerv1pb.Operation) error {
	name, err := ParseGKENameObject(d.parent)
	if err != nil {
		return err
	}
	req := &containerv1pb.GetOperationRequest{
		Name: fmt.Sprintf("projects/%s/locations/%s/operations/%s", name.Projects, name.Locations, op.Name),
	}
	for {
		if op != nil {
			switch op.Status {
			case containerv1pb.Operation_ABORTING:
				return errors.Errorf("operation %s aborting: Detail=%q, StatusMessage=%q",
					op.Name, op.Detail, op.StatusMessage)
			case containerv1pb.Operation_DONE:
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("context cancelled")
		case <-time.After(10 * time.Second):
			d.cmcMu.Lock()
			var err error
			op, err = d.cmc.GetOperation(ctx, req)
			d.cmcMu.Unlock()
			if err != nil {
				glog.Error(err)
			}
		}
	}
}

func diffStrSlice(oldSet, newSet []string) []string {
	oldMap := make(map[string]struct{}, len(oldSet))
	for _, v := range oldSet {
		oldMap[v] = struct{}{}
	}
	var diff []string
	for _, v := range newSet {
		_, ok := oldMap[v]
		if !ok {
			diff = append(diff, v)
		}
	}
	return diff
}

func (d *Driver) addNodeLabel(ctx context.Context, nodeName string, labelKV [2]string) error {
	var ops []jsonpatch.Op
	ops = append(ops, jsonpatch.Op{
		Op:    jsonpatch.OpAdd,
		Path:  "/metadata/labels/" + jsonpatch.EscapeRFC6901(labelKV[0]),
		Value: labelKV[1],
	})
	patchBytes, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	_, err = d.nodeClient.Patch(nodeName, types.JSONPatchType, patchBytes)
	return err
}

func (d *Driver) extendNodePool(ctx context.Context, nodePool string, delta uint, newNodeLabelKV [2]string) error {
	beforeNodes, err := d.getNodesInPool(ctx, nodePool)
	if err != nil {
		return err
	}
	newNodeCount := len(beforeNodes) + int(delta)
	glog.Infof("resizing node pool %s %d->%d", nodePool, len(beforeNodes), newNodeCount)
	resizeOp, err := d.resizeNodePoolOperation(ctx, nodePool, uint(newNodeCount))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if err := d.waitOperationCompletion(ctx, resizeOp); err != nil {
		return err
	}
	glog.Infof("resizing node pool operation done")
	// TODO: use Watch API for getting new nodes!
	afterNodes, err := d.getNodesInPool(ctx, nodePool)
	if err != nil {
		return err
	}
	newNodes := diffStrSlice(beforeNodes, afterNodes)
	if len(newNodes) != int(delta) {
		glog.Infof("expected len(newNodes)=%d, got %d", int(delta), len(newNodes))
	}
	glog.Infof("adding label %v to new nodes %v", newNodeLabelKV, newNodes)
	for _, n := range newNodes {
		if err := d.addNodeLabel(ctx, n, newNodeLabelKV); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) SchedulePod(ctx context.Context, pod *corev1.Pod) ([2]string, error) {
	nodeLabelKV := [2]string{
		"ipp.akihirosuda.github.io/node",
		names.SimpleNameGenerator.GenerateName("ipp-"),
	}
	nodePool, err := d.chooseNodePool(ctx, pod)
	if err != nil {
		return nodeLabelKV, err
	}
	go func() {
		// TODO: use queue and run the routine in Driver.Run()
		d.extendNodeMu.Lock()
		defer d.extendNodeMu.Unlock()
		if err := d.extendNodePool(context.TODO(), nodePool, 1, nodeLabelKV); err != nil {
			glog.Error(err)
			// TODO: remove nodeLabel to recover failed pod
		}
	}()
	return nodeLabelKV, nil
}

// TODO: reuse&remove idle nodes
// To remove a specific node from a node pool, see https://pminkov.github.io/blog/removing-a-node-from-a-kubernetes-cluster-on-gke-google-container-engine.html
