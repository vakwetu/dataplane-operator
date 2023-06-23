/*
Copyright 2023.

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

package deployment

import (
	"context"
	"net"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	dataplanev1beta1 "github.com/openstack-k8s-operators/dataplane-operator/api/v1beta1"
	infranetworkv1 "github.com/openstack-k8s-operators/infra-operator/apis/network/v1beta1"
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	utils "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	baremetalv1 "github.com/openstack-k8s-operators/openstack-baremetal-operator/api/v1beta1"
)

// DeployBaremetalSet Deploy OpenStackBaremetalSet
func DeployBaremetalSet(
	ctx context.Context, helper *helper.Helper, instance *dataplanev1beta1.OpenStackDataPlaneRole,
	nodes *dataplanev1beta1.OpenStackDataPlaneNodeList, ipSets map[string]infranetworkv1.IPSet,
	dnsAddresses []string,
) (bool, error) {
	baremetalSet := &baremetalv1.OpenStackBaremetalSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name,
			Namespace: instance.Namespace,
		},
	}

	utils.LogForObject(helper, "Reconciling BaremetalSet", instance)
	_, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), baremetalSet, func() error {
		instance.Spec.BaremetalSetTemplate.DeepCopyInto(&baremetalSet.Spec)
		for _, node := range nodes.Items {
			ipSet, ok := ipSets[node.Name]
			instanceSpec := baremetalSet.Spec.BaremetalHosts[node.Name]
			if !ok {
				utils.LogForObject(helper, "IPAM Not configured for use, skipping", instance)
				instanceSpec.CtlPlaneIP = node.Spec.AnsibleHost
			} else {
				for _, res := range ipSet.Status.Reservation {
					if res.Network == CtlPlaneNetwork {
						instanceSpec.CtlPlaneIP = res.Address
						baremetalSet.Spec.CtlplaneGateway = *res.Gateway
						baremetalSet.Spec.BootstrapDNS = dnsAddresses
						_, ipNet, err := net.ParseCIDR(res.Cidr)
						if err == nil {
							baremetalSet.Spec.CtlplaneNetmask = net.IP(ipNet.Mask).String()
						}
					}
				}
			}
			baremetalSet.Spec.BaremetalHosts[node.Name] = instanceSpec

		}
		err := controllerutil.SetControllerReference(
			helper.GetBeforeObject(), baremetalSet, helper.GetScheme())
		return err
	})

	if err != nil {
		instance.Status.Conditions.MarkFalse(
			dataplanev1beta1.RoleBareMetalProvisionReadyCondition,
			condition.ErrorReason, condition.SeverityError,
			dataplanev1beta1.RoleBaremetalProvisionErrorMessage)
		return false, err
	}

	// Check if baremetalSet is ready
	if !baremetalSet.IsReady() {
		utils.LogForObject(helper, "BaremetalSet not ready, waiting...", instance)
		return false, nil
	}
	instance.Status.Conditions.MarkTrue(
		dataplanev1beta1.RoleBareMetalProvisionReadyCondition,
		dataplanev1beta1.RoleBaremetalProvisionReadyMessage)
	return true, nil
}

// BuildBMHHostMap  Build managed host map for all roles
func BuildBMHHostMap(ctx context.Context, helper *helper.Helper,
	instance *dataplanev1beta1.OpenStackDataPlane,
	nodes *dataplanev1beta1.OpenStackDataPlaneNodeList,
	roleManagedHostMap map[string]map[string]baremetalv1.InstanceSpec) error {
	for _, node := range nodes.Items {
		labels := node.GetObjectMeta().GetLabels()
		roleName, ok := labels["openstackdataplanerole"]
		if !ok {
			// Node does not have a label
			continue
		}
		if roleManagedHostMap[roleName] == nil {
			roleManagedHostMap[roleName] = make(map[string]baremetalv1.InstanceSpec)
		}

		if !instance.Spec.Roles[roleName].PreProvisioned {
			instanceSpec := baremetalv1.InstanceSpec{}
			instanceSpec.UserData = node.Spec.Node.UserData
			instanceSpec.NetworkData = node.Spec.Node.NetworkData
			roleManagedHostMap[roleName][node.Spec.HostName] = instanceSpec

		}
	}
	return nil
}