package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"sync"

	core "k8s.io/client-go/kubernetes/typed/core/v1"
	extensions "k8s.io/client-go/kubernetes/typed/extensions/v1beta1"
	kubeerrors "k8s.io/client-go/pkg/api/errors"
	api "k8s.io/client-go/pkg/api/v1"
	v1beta1 "k8s.io/client-go/pkg/apis/extensions/v1beta1"

	"github.ibm.com/Bluemix/kubernetes-cpi/agent"
	"github.ibm.com/Bluemix/kubernetes-cpi/config"
	"github.ibm.com/Bluemix/kubernetes-cpi/cpi"
	"github.ibm.com/Bluemix/kubernetes-cpi/kubecluster"
	"k8s.io/client-go/pkg/api/resource"
	"k8s.io/client-go/pkg/api/unversioned"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/util/intstr"
)

type VMCreator struct {
	AgentConfig    *config.Agent
	ClientProvider kubecluster.ClientProvider
}

type Service struct {
	Name           string                  `json:"name"`
	Type           string                  `json:"type"`
	ClusterIP      string                  `json:"cluster_ip"`
	Ports          []Port                  `json:"ports"`
	Selector       map[string]string       `json:"selector"`
	LoadBalancerIP string                  `json:"load_balancer_ip"`
	ExternalIPs    []string                `json:"external_ips"`
	Backend        *v1beta1.IngressBackend `json:"backend"`
	TLS            []v1beta1.IngressTLS    `json:"tls"`
	Rules          []v1beta1.IngressRule   `json:"rules"`
}

type Secret struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Annotations map[string]string `json:"annotations"`
	Data        map[string]string `json:"data"`
	StringData  map[string]string `json:"string_data"`
}

type Port struct {
	Name       string `json:"name"`
	NodePort   int32  `json:"node_port"`
	Port       int32  `json:"port"`
	Protocol   string `json:"protocol"`
	TargetPort int    `json:"target_port"`
}

/*type Backend struct {
	ServiceName string `json:"serviceName"`
	ServicePort string `json:"servicePort"`
}

type TLS struct {
	Hosts      []string `json:"hosts"`
	SecretName string   `json:"secretName"`
}

type Rules struct {
	Host             string `json:"host"`
	IngressRuleValue `json:",inline,omitempty"`
}

type IngressRuleValue struct {
	HTTP *HTTPIngressRuleValue `json:"http,omitempty"`
}

type HTTPIngressRuleValue struct {
	Paths []Path `json:"paths"`
}

type Path struct {
	Path    string  `json:"path,omitempty"`
	Backend Backend `json:"backend"`
}*/

type ResourceName string

const (
	ResourceCPU    ResourceName = "cpu"
	ResourceMemory ResourceName = "memory"
)

var ProgressDeadlineSeconds int32 = 30

type ResourceList map[ResourceName]string

type Resources struct {
	Limits   ResourceList `json:"limits"`
	Requests ResourceList `json:"requests"`
}

type VMCloudProperties struct {
	Context   string    `json:"context"`
	Services  []Service `json:"services,omitempty"`
	Secrets   []Secret  `json:"secrets,omitempty"`
	Resources Resources `json:"resources,omitempty"`
	Replicas  *int32    `json:"replicas"`
}

func (v *VMCreator) Create(
	agentID string,
	stemcellCID cpi.StemcellCID,
	cloudProps VMCloudProperties,
	networks cpi.Networks,
	diskCIDs []cpi.DiskCID,
	env cpi.Environment,
) (cpi.VMCID, error) {

	// only one network is supported
	network, err := getNetwork(networks)
	if err != nil {
		return "", err
	}

	// create the client set
	client, err := v.ClientProvider.New(cloudProps.Context)
	if err != nil {
		return "", err
	}

	// create the target namespace if it doesn't already exist
	err = createNamespace(client.Core(), client.Namespace())
	if err != nil {
		return "", err
	}

	// NOTE: This is a workaround for the fake Clientset. This should be
	// removed once https://github.com/kubernetes/client-go/issues/48 is
	// resolved.
	ns := client.Namespace()
	instanceSettings, err := v.InstanceSettings(agentID, networks, env)
	if err != nil {
		return "", err
	}

	// create the config map
	_, err = createConfigMap(client.ConfigMaps(), ns, agentID, instanceSettings)
	if err != nil {
		return "", err
	}

	// create the service
	err = createServices(client, ns, agentID, cloudProps.Services)
	if err != nil {
		return "", err
	}

	err = createSecret(client.Core(), ns, agentID, cloudProps.Secrets)
	if err != nil {
		return "", err
	}

	if cloudProps.Replicas == nil {
		// create the pod
		_, err = createPod(client.Pods(), ns, agentID, string(stemcellCID), *network, cloudProps.Resources)
		if err != nil {
			return "", err
		}
	} else if *cloudProps.Replicas >= 1 {
		// create the deployments
		_, err := createDeployment(client.Deployments(), ns, agentID, string(stemcellCID), *network, cloudProps)
		if err != nil {
			return "", err
		}
	} else {
		return "", errors.New("Invalid number of Replicas specified in Cloud Properties")
	}

	return NewVMCID(client.Context(), agentID), nil
}

func getNetwork(networks cpi.Networks) (*cpi.Network, error) {
	switch len(networks) {
	case 0:
		return nil, errors.New("a network is required")
	case 1:
		for _, nw := range networks {
			return &nw, nil
		}
	default:
		return nil, errors.New("multiple networks not supported")
	}

	panic("unreachable")
}

func (v *VMCreator) InstanceSettings(agentID string, networks cpi.Networks, env cpi.Environment) (*agent.Settings, error) {
	agentNetworks := agent.Networks{}
	for name, cpiNetwork := range networks {
		agentNetwork := agent.Network{}
		if err := cpi.Remarshal(cpiNetwork, &agentNetwork); err != nil {
			return nil, err
		}
		agentNetwork.Preconfigured = true
		agentNetworks[name] = agentNetwork
	}

	settings := &agent.Settings{
		Blobstore:  v.AgentConfig.Blobstore,
		MessageBus: v.AgentConfig.MessageBus,
		NTPServers: v.AgentConfig.NTPServers,

		AgentID: agentID,
		VM:      agent.VM{Name: agentID},

		Env:      env,
		Networks: agentNetworks,
		Disks:    agent.Disks{},
	}
	return settings, nil
}

func createNamespace(coreClient core.CoreV1Interface, namespace string) error {
	_, err := coreClient.Namespaces().Get(namespace)
	if err == nil {
		return nil
	}

	_, err = coreClient.Namespaces().Create(&v1.Namespace{
		ObjectMeta: v1.ObjectMeta{Name: namespace},
	})
	if err == nil {
		return nil
	}

	if statusError, ok := err.(*kubeerrors.StatusError); ok {
		if statusError.Status().Reason == unversioned.StatusReasonAlreadyExists {
			return nil
		}
	}
	return err
}

func createConfigMap(configMapService core.ConfigMapInterface, ns, agentID string, instanceSettings *agent.Settings) (*v1.ConfigMap, error) {
	instanceJSON, err := json.Marshal(instanceSettings)
	if err != nil {
		return nil, err
	}

	return configMapService.Create(&v1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      "agent-" + agentID,
			Namespace: ns,
			Labels: map[string]string{
				"bosh.cloudfoundry.org/agent-id": agentID,
			},
		},
		Data: map[string]string{
			"instance_settings": string(instanceJSON),
		},
	})
}

func createServices(client kubecluster.Client, ns, agentID string, services []Service) error {
	var err error
	for _, svc := range services {
		var serviceType v1.ServiceType
		var lock *sync.Mutex = &sync.Mutex{}

		switch svc.Type {
		default:
			serviceType = v1.ServiceTypeClusterIP
		case "NodePort":
			serviceType = v1.ServiceTypeNodePort
		case "LoadBalancer":
			serviceType = v1.ServiceTypeLoadBalancer
		}
		var ports []v1.ServicePort
		for _, port := range svc.Ports {
			port := v1.ServicePort{
				Name:       port.Name,
				Protocol:   v1.Protocol(port.Protocol),
				Port:       port.Port,
				NodePort:   port.NodePort,
				TargetPort: intstr.FromInt(port.TargetPort),
			}
			ports = append(ports, port)
		}

		objectMeta := v1.ObjectMeta{
			Name:      svc.Name,
			Namespace: ns,
			Labels: map[string]string{
				"bosh.cloudfoundry.org/agent-id": agentID,
			},
		}

		if svc.Type == "Ingress" {
			service := &v1beta1.Ingress{
				ObjectMeta: objectMeta,
				Spec: v1beta1.IngressSpec{
					Backend: svc.Backend,
					TLS:     svc.TLS,
					Rules:   svc.Rules,
				},
			}
			_, err = client.IngressService().Create(service)
			if err != nil {
				return err
			}
		} else {
			service := &v1.Service{
				ObjectMeta: objectMeta,
				Spec: v1.ServiceSpec{
					Type:        serviceType,
					ClusterIP:   svc.ClusterIP,
					Ports:       ports,
					ExternalIPs: svc.ExternalIPs,
				},
			}

			if service.Spec.Type == v1.ServiceTypeLoadBalancer && len(svc.LoadBalancerIP) != 0 {
				service.Spec.LoadBalancerIP = svc.LoadBalancerIP
			}

			if len(svc.Selector) != 0 {
				service.Spec.Selector = svc.Selector
			} else {
				service.Spec.Selector = map[string]string{
					"bosh.cloudfoundry.org/agent-id": agentID,
				}
			}

			lock.Lock()
			if _, err = client.Services().Get(svc.Name); err != nil {
				_, err = client.Services().Create(service)
				if err != nil {
					return err
				}
			}
			lock.Unlock()
		}
	}
	return nil
}

func createSecret(coreClient core.CoreV1Interface, ns, agentID string, secrets []Secret) error {
	var err error
	for _, srt := range secrets {
		if _, err := coreClient.Secrets(ns).Get(srt.Name); err == nil {
			return errors.New("Secret name " + srt.Name + " already exists.")
		}

		var secretType v1.SecretType
		switch srt.Type {
		default:
			secretType = v1.SecretTypeOpaque
		case "DockerCfg":
			secretType = v1.SecretTypeDockercfg
		case "ServiceAccountToken":
			secretType = v1.SecretTypeServiceAccountToken
		case "TLS":
			secretType = v1.SecretTypeTLS
		}

		data := make(map[string][]byte)
		for k, v := range srt.Data {
			if k == ".dockercfg" {
				if data[k], err = ioutil.ReadFile(v); err != nil {
					return err
				}
			} else {
				data[k] = []byte(v)
			}
		}

		objectMeta := v1.ObjectMeta{
			Name:      srt.Name,
			Namespace: ns,
			Labels: map[string]string{
				"bosh.cloudfoundry.org/agent-id": agentID,
			},
			Annotations: srt.Annotations,
		}

		secret := &v1.Secret{
			ObjectMeta: objectMeta,
			Data:       data,
			StringData: srt.StringData,
			Type:       secretType,
		}

		_, err = coreClient.Secrets(ns).Create(secret)
		if err != nil {
			return err
		}
	}

	return nil
}

func createPod(podClient core.PodInterface, ns, agentID, image string, network cpi.Network, resources Resources) (*v1.Pod, error) {
	trueValue := true
	rootUID := int64(0)

	annotations := map[string]string{}
	if len(network.IP) > 0 {
		annotations["bosh.cloudfoundry.org/ip-address"] = network.IP
	}

	resourceReqs, err := getPodResourceRequirements(resources)
	if err != nil {
		return nil, err
	}

	return podClient.Create(&v1.Pod{
		ObjectMeta: v1.ObjectMeta{
			Name:        "agent-" + agentID,
			Namespace:   ns,
			Annotations: annotations,
			Labels: map[string]string{
				"bosh.cloudfoundry.org/agent-id": agentID,
			},
		},
		Spec: v1.PodSpec{
			Hostname: agentID,
			Containers: []v1.Container{{
				Name:            "bosh-job",
				Image:           image,
				ImagePullPolicy: v1.PullAlways,
				Command:         []string{"/usr/sbin/runsvdir-start"},
				Args:            []string{},
				Resources:       resourceReqs,
				SecurityContext: &v1.SecurityContext{
					Privileged: &trueValue,
					RunAsUser:  &rootUID,
				},
				VolumeMounts: []v1.VolumeMount{{
					Name:      "bosh-config",
					MountPath: "/var/vcap/bosh/instance_settings.json",
					SubPath:   "instance_settings.json",
				}, {
					Name:      "bosh-ephemeral",
					MountPath: "/var/vcap/data",
				}},
			}},
			Volumes: []v1.Volume{{
				Name: "bosh-config",
				VolumeSource: v1.VolumeSource{
					ConfigMap: &v1.ConfigMapVolumeSource{
						LocalObjectReference: v1.LocalObjectReference{
							Name: "agent-" + agentID,
						},
						Items: []v1.KeyToPath{{
							Key:  "instance_settings",
							Path: "instance_settings.json",
						}},
					},
				},
			}, {
				Name: "bosh-ephemeral",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			}},
		},
	})
}

func createDeployment(deploymentClient extensions.DeploymentInterface,
	ns, agentID, image string,
	network cpi.Network,
	cloudProps VMCloudProperties,
) (*v1beta1.Deployment, error) {
	trueValue := true
	rootUID := int64(0)

	annotations := map[string]string{}
	if len(network.IP) > 0 {
		annotations["bosh.cloudfoundry.org/ip-address"] = network.IP
	}

	resourceReqs, err := getPodResourceRequirements(cloudProps.Resources)
	if err != nil {
		return nil, err
	}
	deployment := &v1beta1.Deployment{
		ObjectMeta: api.ObjectMeta{
			Name:      "agent-" + agentID,
			Namespace: ns,
		},
		Spec: v1beta1.DeploymentSpec{
			Replicas: cloudProps.Replicas,
			Template: v1.PodTemplateSpec{
				ObjectMeta: api.ObjectMeta{
					Labels: map[string]string{
						"app": "demo", // TODO
					},
				},
				Spec: v1.PodSpec{
					Hostname: agentID,
					Containers: []v1.Container{{
						Name:            "bosh-job",
						Image:           image,
						ImagePullPolicy: v1.PullAlways,
						Command:         []string{"/usr/sbin/runsvdir-start"},
						Args:            []string{},
						Resources:       resourceReqs,
						SecurityContext: &v1.SecurityContext{
							Privileged: &trueValue,
							RunAsUser:  &rootUID,
						},
						VolumeMounts: []v1.VolumeMount{{
							Name:      "bosh-config",
							MountPath: "/var/vcap/bosh/instance_settings.json",
							SubPath:   "instance_settings.json",
						}, {
							Name:      "bosh-ephemeral",
							MountPath: "/var/vcap/data",
						}},
					}},
					Volumes: []v1.Volume{{
						Name: "bosh-config",
						VolumeSource: v1.VolumeSource{
							ConfigMap: &v1.ConfigMapVolumeSource{
								LocalObjectReference: v1.LocalObjectReference{
									Name: "agent-" + agentID,
								},
								Items: []v1.KeyToPath{{
									Key:  "instance_settings",
									Path: "instance_settings.json",
								}},
							},
						},
					}, {
						Name: "bosh-ephemeral",
						VolumeSource: v1.VolumeSource{
							EmptyDir: &v1.EmptyDirVolumeSource{},
						},
					}},
				},
			},
			ProgressDeadlineSeconds: &ProgressDeadlineSeconds,
		},
	}

	return deploymentClient.Create(deployment)
}

func getPodResourceRequirements(resources Resources) (v1.ResourceRequirements, error) {
	limits, err := getResourceList(resources.Limits)
	if err != nil {
		return v1.ResourceRequirements{}, err
	}

	requests, err := getResourceList(resources.Requests)
	if err != nil {
		return v1.ResourceRequirements{}, err
	}

	return v1.ResourceRequirements{Limits: limits, Requests: requests}, nil
}

func getResourceList(resourceList ResourceList) (v1.ResourceList, error) {
	if resourceList == nil {
		return nil, nil
	}

	list := v1.ResourceList{}
	for k, v := range resourceList {
		quantity, err := resource.ParseQuantity(v)
		if err != nil {
			return nil, err
		}

		name, err := kubeResourceName(k)
		if err != nil {
			return nil, err
		}
		list[name] = quantity
	}

	return list, nil
}

func kubeResourceName(name ResourceName) (v1.ResourceName, error) {
	switch name {
	case ResourceMemory:
		return v1.ResourceMemory, nil
	case ResourceCPU:
		return v1.ResourceCPU, nil
	default:
		return "", fmt.Errorf("%s is not a supported resource type", name)
	}
}
