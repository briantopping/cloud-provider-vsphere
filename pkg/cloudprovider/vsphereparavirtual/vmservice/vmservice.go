/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vmservice

import (
	"context"
	"crypto/md5" // #nosec
	"encoding/hex"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rest "k8s.io/client-go/rest"

	vmopv1 "github.com/vmware-tanzu/vm-operator/api/v1alpha2"
	vmop "k8s.io/cloud-provider-vsphere/pkg/cloudprovider/vsphereparavirtual/vmoperator"
	vmopclient "k8s.io/cloud-provider-vsphere/pkg/cloudprovider/vsphereparavirtual/vmoperator/client"
)

const (
	// ClusterSelectorKey expects key/value pair {ClusterSelectorKey: <cluster name>} for target nodes: ClusterSelectorKey
	ClusterSelectorKey = "capv.vmware.com/cluster.name"
	// NodeSelectorKey expects key/value pair {NodeSelectorKey: NodeRole} for target nodes: NodeSelectorKey
	NodeSelectorKey = "capv.vmware.com/cluster.role"

	// LegacyClusterSelectorKey expects key/value pair {LegacyClusterSelectorKey: <cluster name>} for target nodes: LegacyClusterSelectorKey
	LegacyClusterSelectorKey = "capw.vmware.com/cluster.name"
	// LegacyNodeSelectorKey expects key/value pair {LegacyNodeSelectorKey: NodeRole} for target nodes: LegacyNodeSelectorKey
	LegacyNodeSelectorKey = "capw.vmware.com/cluster.role"

	// NodeRole is set by capw, we are targeting worker vms
	NodeRole = "node"

	// LabelClusterNameKey label should be added on virtual machine service with its corresponding k8s service
	LabelClusterNameKey = "run.tanzu.vmware.com/cluster.name"
	// LabelServiceNameKey label should be added on virtual machine service with its corresponding k8s service
	LabelServiceNameKey = "run.tanzu.vmware.com/service.name"
	// LabelServiceNameSpaceKey label should be added on virtual machine service with its corresponding k8s service
	LabelServiceNameSpaceKey = "run.tanzu.vmware.com/service.namespace"

	// AnnotationServiceExternalTrafficPolicyKey label is used to piggyback vSphere Paravirtual Service's
	// configuration to the supervisor cluster. AnnotationServiceExternalTrafficPolicyKey and AnnotationServiceHealthCheckNodePortKey are not part of
	// VirtualMachineService spec because they're K8s Service/Pod specific and
	// don't apply in a VirtualMachine context
	AnnotationServiceExternalTrafficPolicyKey = "virtualmachineservice.vmoperator.vmware.com/service.externalTrafficPolicy"
	// AnnotationServiceHealthCheckNodePortKey label is used to piggyback vSphere Paravirtual Service's
	// configuration to the supervisor cluster.
	AnnotationServiceHealthCheckNodePortKey = "virtualmachineservice.vmoperator.vmware.com/service.healthCheckNodePort"

	// AnnotationLastAppliedConfiguration is used by kubectl as a legacy mechanism to track changes.
	// That mechanism has been superseded by Server-side apply.
	AnnotationLastAppliedConfiguration = "kubectl.kubernetes.io/last-applied-configuration"

	// MaxCheckSumLen is the maximum length of vmservice suffix: vsphere paravirtual name length cannot exceed 41 bytes in total, so we need to make sure vmservice suffix is 21 bytes (63 - 41 -1 = 21)
	// https://gitlab.eng.vmware.com/core-build/guest-cluster-controller/blob/master/webhooks/validation/tanzukubernetescluster_validator.go#L56
	MaxCheckSumLen = 21
)

var excludedAnnotations = []string{
	AnnotationLastAppliedConfiguration,
	AnnotationServiceExternalTrafficPolicyKey,
	AnnotationServiceHealthCheckNodePortKey,
}

// A list of possible error messages
var (
	ErrCreateVMService     = errors.New("failed to create VirtualMachineService")
	ErrUpdateVMService     = errors.New("failed to update VirtualMachineService")
	ErrGetVMService        = errors.New("failed to get VirtualMachineService")
	ErrDeleteVMService     = errors.New("failed to delete VirtualMachineService")
	ErrVMServiceIPNotFound = errors.New("VirtualMachineService IP not found")
	ErrNodePortNotFound    = errors.New("NodePort not found")
)

var (
	// IsLegacy indicates whether legacy paravirtual mode is enabled
	// Default to false
	IsLegacy bool
)

// GetVmopClient gets a vm-operator-api client
// This is separate from NewVMService so that a fake client can be injected for testing
func GetVmopClient(config *rest.Config) (vmop.Interface, error) {
	return vmopclient.NewForConfig(config)
}

// NewVMService creates a vmService object
func NewVMService(vmClient vmop.Interface, ns string, ownerRef *metav1.OwnerReference, serviceAnnotationPropagationEnabled bool) VMService {
	return &vmService{
		vmClient:                            vmClient,
		namespace:                           ns,
		ownerReference:                      ownerRef,
		serviceAnnotationPropagationEnabled: serviceAnnotationPropagationEnabled,
	}
}

func (s *vmService) hashString(str string) string {
	// #nosec
	hash := md5.New()
	if _, err := hash.Write([]byte(str)); err != nil {
		log.Error(err, "create hash string failed")
	}

	return hex.EncodeToString(hash.Sum(nil))
}

// GetVMServiceName returns VirtualMachineService name for a lb type of service
func (s *vmService) GetVMServiceName(service *v1.Service, clusterName string) string {
	suffix := s.hashString(service.Name + "." + service.Namespace)
	logger := log.WithValues("name", service.Name, "namespace", service.Namespace)
	logger.V(6).Info(fmt.Sprintf("Hash string for VirtualMachinService Name is %s", suffix))

	if len(suffix) > MaxCheckSumLen {
		suffix = suffix[:MaxCheckSumLen]
		logger.V(6).Info(fmt.Sprintf("Hash string for VirtualMachinService Name is truncated to %s", suffix))
	}
	return clusterName + "-" + suffix
}

// Get returns the corresponding virtual machine service if it exists
func (s *vmService) Get(ctx context.Context, service *v1.Service, clusterName string) (*vmopv1.VirtualMachineService, error) {
	logger := log.WithValues("name", service.Name, "namespace", service.Namespace)
	logger.V(2).Info("Attempting to get VirtualMachineService")

	vmService, err := s.vmClient.V1alpha2().VirtualMachineServices(s.namespace).Get(ctx, s.GetVMServiceName(service, clusterName), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		logger.Error(ErrGetVMService, fmt.Sprintf("%v", err))
		return nil, err
	}

	return vmService, nil
}

// Create creates a vmservice to map to the given lb type of service, it should be called if vmservice not found
func (s *vmService) Create(ctx context.Context, service *v1.Service, clusterName string) (*vmopv1.VirtualMachineService, error) {
	logger := log.WithValues("name", service.Name, "namespace", service.Namespace)
	logger.V(2).Info("Attempting to create VirtualMachineService")

	vmService, err := s.lbServiceToVMService(service, clusterName)
	if err != nil {
		logger.Error(ErrCreateVMService, fmt.Sprintf("%v", err))
		return nil, err
	}

	vmService, err = s.vmClient.V1alpha2().VirtualMachineServices(s.namespace).Create(ctx, vmService, metav1.CreateOptions{})
	if err != nil {
		logger.Error(ErrCreateVMService, fmt.Sprintf("%v", err))
		return nil, err
	}

	logger.V(2).Info("Successfully created VirtualMachineService")

	return vmService, nil
}

// CreateOrUpdate creates a vmservice to map to the given lb type of service
func (s *vmService) CreateOrUpdate(ctx context.Context, service *v1.Service, clusterName string) (*vmopv1.VirtualMachineService, error) {
	logger := log.WithValues("name", service.Name, "namespace", service.Namespace)
	logger.V(2).Info("Attempting to create or update a VirtualMachineService")

	if clusterName == "" {
		logger.Error(ErrCreateVMService, "cluster name is required to create or update a vm service")
		return nil, errors.Wrapf(ErrCreateVMService, "cluster name cannot be empty")
	}

	vmService, err := s.Get(ctx, service, clusterName)
	if err != nil {
		return nil, err
	}

	if vmService == nil {
		// Create a new VirtualMachineService if not found
		vmService, err = s.Create(ctx, service, clusterName)
		if err != nil {
			logger.Error(ErrCreateVMService, fmt.Sprintf("%v", err))
			return nil, err
		}
	} else {
		// Update the existing VirtualMachineService
		vmService, err = s.Update(ctx, service, clusterName, vmService)
		if err != nil {
			logger.Error(ErrUpdateVMService, fmt.Sprintf("%v", err))
			return nil, err
		}
	}

	vmServiceIP := getVMServiceIP(vmService)
	if vmServiceIP == "" {
		return vmService, ErrVMServiceIPNotFound
	}

	logger.V(2).Info("VirtualMachineService IP has been found")

	return vmService, err
}

// Update updates a vmservice
func (s *vmService) Update(ctx context.Context, service *v1.Service, clusterName string, vmService *vmopv1.VirtualMachineService) (*vmopv1.VirtualMachineService, error) {
	logger := log.WithValues("name", service.Name, "namespace", service.Namespace)
	logger.V(2).Info("Attempting to update VirtualMachineService")

	// Compare the ports setting in service and vmService, update vmService if needed
	ports, err := findPorts(service)
	if err != nil {
		logger.Error(ErrUpdateVMService, fmt.Sprintf("%v", err))
		return nil, err
	}
	vmServicePorts := vmService.Spec.Ports

	newVMService := vmService.DeepCopy()

	if vmService.Spec.LoadBalancerSourceRanges == nil {
		vmService.Spec.LoadBalancerSourceRanges = []string{}
	}
	if service.Spec.LoadBalancerSourceRanges == nil {
		service.Spec.LoadBalancerSourceRanges = []string{}
	}

	annotations := getVMServiceAnnotations(vmService, service, s.serviceAnnotationPropagationEnabled)

	// VMService only has a few fields to be kept in sync so we will simply
	// iterate over them
	// As more fields are added, we need to consider adopting a patch helper
	var needsUpdate bool
	if !reflect.DeepEqual(vmServicePorts, ports) {
		needsUpdate = true
		newVMService.Spec.Ports = ports
	}
	if vmService.Spec.LoadBalancerIP != service.Spec.LoadBalancerIP {
		needsUpdate = true
		newVMService.Spec.LoadBalancerIP = service.Spec.LoadBalancerIP
	}
	if !reflect.DeepEqual(vmService.Spec.LoadBalancerSourceRanges, service.Spec.LoadBalancerSourceRanges) {
		needsUpdate = true
		newVMService.Spec.LoadBalancerSourceRanges = service.Spec.LoadBalancerSourceRanges
	}
	if !reflect.DeepEqual(vmService.Annotations, annotations) {
		needsUpdate = true
		newVMService.Annotations = annotations
	}

	if needsUpdate {
		newVMService, err = s.vmClient.V1alpha2().VirtualMachineServices(s.namespace).Update(ctx, newVMService, metav1.UpdateOptions{})
		if err != nil {
			logger.Error(ErrUpdateVMService, fmt.Sprintf("%v", err))
			return nil, err
		}

		logger.V(2).Info("Successfully updated VirtualMachineService")
		return newVMService, nil
	}

	return vmService, nil
}

// Delete deletes the vmservice mapped to the given lb type of service
func (s *vmService) Delete(ctx context.Context, service *v1.Service, clusterName string) error {
	logger := log.WithValues("name", service.Name, "namespace", service.Namespace)
	logger.V(2).Info("Attempting to delete VirtualMachineService")

	err := s.vmClient.V1alpha2().VirtualMachineServices(s.namespace).Delete(ctx, s.GetVMServiceName(service, clusterName), metav1.DeleteOptions{})
	if err != nil {
		logger.Error(ErrDeleteVMService, fmt.Sprintf("%v", err))
		return err
	}

	logger.V(2).Info("Successfully deleted VirtualMachineService")
	return nil
}

func findPorts(service *v1.Service) ([]vmopv1.VirtualMachineServicePort, error) {
	var ports []vmopv1.VirtualMachineServicePort
	for _, port := range service.Spec.Ports {
		if port.NodePort == 0 {
			return nil, errors.Wrapf(ErrNodePortNotFound, "port %s", port.Name)
		}
		ports = append(ports, vmopv1.VirtualMachineServicePort{
			Name:       port.Name,
			Port:       port.Port,
			TargetPort: port.NodePort,
			Protocol:   string(port.Protocol),
		})
	}
	return ports, nil
}

func (s *vmService) lbServiceToVMService(service *v1.Service, clusterName string) (*vmopv1.VirtualMachineService, error) {
	ports, err := findPorts(service)
	if err != nil {
		return nil, err
	}
	vmServiceSpec := vmopv1.VirtualMachineServiceSpec{
		Type:  vmopv1.VirtualMachineServiceTypeLoadBalancer,
		Ports: ports,
		Selector: map[string]string{
			ClusterSelectorKey: clusterName,
			NodeSelectorKey:    NodeRole,
		},
		// When service has spec.loadBalancerIP specified, pass it to the
		// corresponding VirtualMachineService
		LoadBalancerIP: service.Spec.LoadBalancerIP,
		// When service has spec.LoadBalancerSourceRanges specified,
		// pass it to the corresponding VirtualMachineService
		LoadBalancerSourceRanges: service.Spec.LoadBalancerSourceRanges,
	}

	if IsLegacy {
		vmServiceSpec.Selector = map[string]string{
			LegacyClusterSelectorKey: clusterName,
			LegacyNodeSelectorKey:    NodeRole,
		}
	}

	label := map[string]string{
		LabelClusterNameKey:      clusterName,
		LabelServiceNameKey:      service.Name,
		LabelServiceNameSpaceKey: service.Namespace,
	}

	vmService := &vmopv1.VirtualMachineService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: vmopclient.VirtualMachineServiceGVR.Group + "/" + vmopclient.VirtualMachineServiceGVR.Version,
			Kind:       "VirtualMachineService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels: label,
			Name:   s.GetVMServiceName(service, clusterName),
			OwnerReferences: []metav1.OwnerReference{
				*s.ownerReference,
			},
		},
		Spec: vmServiceSpec,
	}

	if annotations := getVMServiceAnnotations(vmService, service, s.serviceAnnotationPropagationEnabled); len(annotations) != 0 {
		vmService.Annotations = annotations
	}

	return vmService, nil
}

func getVMServiceAnnotations(vmService *vmopv1.VirtualMachineService, service *v1.Service, serviceAnnotationPropagationEnabled bool) map[string]string {
	var annotations map[string]string
	// When ExternalTrafficPolicy is set to Local in the Service, add its
	// value and the healthCheckNodePort to VirtualMachineService
	// labels
	// When ExternalTrafficPolicy is set to Cluster, do nothing as that's
	// the default value, also there will be no HealthCheckNodePort
	// allocated in that case
	if service.Spec.ExternalTrafficPolicy == v1.ServiceExternalTrafficPolicyTypeLocal {
		annotations = make(map[string]string)
		annotations[AnnotationServiceExternalTrafficPolicyKey] = string(service.Spec.ExternalTrafficPolicy)
		annotations[AnnotationServiceHealthCheckNodePortKey] = strconv.Itoa(int(service.Spec.HealthCheckNodePort))
	}

	// Annotation propagation logic
	if serviceAnnotationPropagationEnabled {
		// Initialize annotations map if empty
		if annotations == nil {
			annotations = make(map[string]string)
		}
		// Merge service annotations
		for k, v := range service.Annotations {
			if !slices.Contains(excludedAnnotations, k) {
				annotations[k] = v
			}
		}
	}

	return annotations
}

func getVMServiceIP(vmService *vmopv1.VirtualMachineService) string {
	if len(vmService.Status.LoadBalancer.Ingress) > 0 {
		return vmService.Status.LoadBalancer.Ingress[0].IP
	}
	return ""
}
