package controllers

import (
	"context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	stdlog "log"
	"os"
	"testing"

	tlog "github.com/go-logr/logr/testing"
	"github.com/open-cluster-management/multicloud-operators-foundation/pkg/klusterlet/agent"
	routev1Fake "github.com/openshift/client-go/route/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/kubernetes/scheme"

	extensionv1beta1 "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	clusterv1beta1 "github.com/open-cluster-management/multicloud-operators-foundation/pkg/apis/internal.open-cluster-management.io/v1beta1"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	kubeNode = &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"kubernetes.io/arch":             "amd64",
				"kubernetes.io/os":               "linux",
				"node-role.kubernetes.io/worker": "",
				"node.openshift.io/os_id":        "rhcos",
			},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.Quantity{},
				corev1.ResourceMemory: resource.Quantity{},
			},
			Conditions: []corev1.NodeCondition{
				{
					Type: corev1.NodeReady,
				},
			},
		},
	}

	ocpConsole = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "console-config",
			Namespace: "openshift-console",
		},
		Data: map[string]string{
			"console-config.yaml": "apiVersion: console.openshift.io/v1\nauth:\n" +
				"clientID: console\n  clientSecretFile: /var/oauth-config/clientSecret\n" +
				"oauthEndpointCAFile: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt\n" +
				"clusterInfo:\n  consoleBaseAddress: https://console-openshift-console.apps.daliu-clu428.dev04.red-chesterfield.com\n" +
				"masterPublicURL: https://api.daliu-clu428.dev04.red-chesterfield.com:6443\ncustomization:\n" +
				"branding: ocp\n  documentationBaseURL: https://docs.openshift.com/container-platform/4.3/\n" +
				"kind: ConsoleConfig\nproviders: {}\nservingInfo:\n  bindAddress: https://[::]:8443\n" +
				"certFile: /var/serving-cert/tls.crt\n  keyFile: /var/serving-cert/tls.key\n",
		},
	}
	kubeEndpoints = &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: "127.0.0.1",
					},
				},
				Ports: []corev1.EndpointPort{
					{
						Port: 443,
					},
				},
			},
		},
	}

	kubeMonitoringSecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "monitoring",
			Namespace: "kube-system",
		},
		Data: map[string][]byte{
			"tls.crt": []byte("aaa"),
			"tls.key": []byte("aaa"),
		},
	}
	agentIngress = &extensionv1beta1.Ingress{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Ingress",
			APIVersion: "extension/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "foundation-ingress-testcluster-agent",
			Namespace:         "kube-system",
			CreationTimestamp: metav1.Now(),
		},
		Spec: extensionv1beta1.IngressSpec{
			Rules: []extensionv1beta1.IngressRule{
				{
					Host: "test.com",
				},
			},
		},
		Status: extensionv1beta1.IngressStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{
					{
						IP: "127.0.0.1",
					},
				},
			},
		},
	}

	agentService = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agent",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{
				{
					Port:       8080,
					TargetPort: intstr.FromInt(80),
				},
			},
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{
					{
						IP: "10.0.0.1",
					},
				},
			},
		},
	}
)
var clusterInfoNamespace = "cn1"
var clusterInfoName = "c1"
var clusterc1Request = reconcile.Request{
	NamespacedName: types.NamespacedName{
		Name: clusterInfoName, Namespace: clusterInfoNamespace}}

var cfg *rest.Config

func TestMain(m *testing.M) {
	t := &envtest.Environment{}
	var err error
	if cfg, err = t.Start(); err != nil {
		stdlog.Fatal(err)
	}
	code := m.Run()
	t.Stop()
	os.Exit(code)
}

func TestClusterInfoReconcile(t *testing.T) {
	// Create new cluster
	now := metav1.Now()
	clusterInfo := &clusterv1beta1.ManagedClusterInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name:              clusterInfoName,
			Namespace:         clusterInfoNamespace,
			CreationTimestamp: now,
		},
	}

	s := scheme.Scheme
	s.AddKnownTypes(clusterv1beta1.GroupVersion, &clusterv1beta1.ManagedClusterInfo{})
	clusterv1beta1.AddToScheme(s)

	c := fake.NewFakeClientWithScheme(s, clusterInfo)

	fr := NewClusterInfoReconciler()

	fr.Client = c
	fr.Agent = agent.NewAgent("c1", fr.KubeClient)

	_, err := fr.Reconcile(clusterc1Request)
	if err != nil {
		t.Errorf("Failed to run reconcile cluster. error: %v", err)
	}

	updatedClusterInfo := &clusterv1beta1.ManagedClusterInfo{}
	err = fr.Get(context.Background(), clusterc1Request.NamespacedName, updatedClusterInfo)
	if err != nil {
		t.Errorf("failed get updated clusterinfo ")
	}

	if meta.IsStatusConditionFalse(updatedClusterInfo.Status.Conditions, clusterv1beta1.ManagedClusterInfoSynced) {
		t.Errorf("failed to update synced condtion")
	}
}
func NewClusterInfoReconciler() *ClusterInfoReconciler {
	fakeKubeClient := kubefake.NewSimpleClientset(
		kubeNode, kubeEndpoints, ocpConsole, agentIngress, kubeMonitoringSecret, agentService)
	fakeRouteV1Client := routev1Fake.NewSimpleClientset()
	return &ClusterInfoReconciler{
		Log:           tlog.NullLogger{},
		KubeClient:    fakeKubeClient,
		RouteV1Client: fakeRouteV1Client,
		AgentAddress:  "127.0.0.1:8000",
		AgentIngress:  "kube-system/foundation-ingress-testcluster-agent",
		AgentRoute:    "AgentRoute",
		AgentService:  "kube-system/agent",
	}
}

func TestFailedClusterInfoReconcile(t *testing.T) {
	// Create new cluster
	now := metav1.Now()
	clusterInfo := &clusterv1beta1.ManagedClusterInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name:              clusterInfoName,
			Namespace:         clusterInfoNamespace,
			CreationTimestamp: now,
		},
	}

	s := scheme.Scheme
	s.AddKnownTypes(clusterv1beta1.GroupVersion, &clusterv1beta1.ManagedClusterInfo{})
	clusterv1beta1.AddToScheme(s)

	c := fake.NewFakeClientWithScheme(s, clusterInfo)

	fr := NewFailedClusterInfoReconciler()

	fr.Client = c
	fr.Agent = agent.NewAgent("c1", fr.KubeClient)

	_, err := fr.Reconcile(clusterc1Request)
	if err != nil {
		t.Errorf("Failed to run reconcile cluster. error: %v", err)
	}

	updatedClusterInfo := &clusterv1beta1.ManagedClusterInfo{}
	err = fr.Get(context.Background(), clusterc1Request.NamespacedName, updatedClusterInfo)
	if err != nil {
		t.Errorf("failed get updated clusterinfo ")
	}

	if meta.IsStatusConditionTrue(updatedClusterInfo.Status.Conditions, clusterv1beta1.ManagedClusterInfoSynced) {
		t.Errorf("failed to update synced condtion")
	}
}

func NewFailedClusterInfoReconciler() *ClusterInfoReconciler {
	fakeKubeClient := kubefake.NewSimpleClientset(
		kubeNode, ocpConsole, kubeMonitoringSecret)
	fakeRouteV1Client := routev1Fake.NewSimpleClientset()
	return &ClusterInfoReconciler{
		Log:           tlog.NullLogger{},
		KubeClient:    fakeKubeClient,
		RouteV1Client: fakeRouteV1Client,
		AgentAddress:  "127.0.0.1:8000",
		AgentIngress:  "kube-system/foundation-ingress-testcluster-agent",
		AgentRoute:    "AgentRoute",
		AgentService:  "kube-system/agent",
	}
}

func TestClusterInfoReconciler_getMasterAddresses(t *testing.T) {
	cir := NewClusterInfoReconciler()
	endpointaddr, endpointport, clusterurl := cir.getMasterAddresses()
	if len(endpointaddr) < 1 || len(endpointport) < 1 {
		t.Errorf("Failed to get clusterinfo. endpointaddr:%v, endpointport:%v, clusterurl:%v", endpointaddr, endpointport, clusterurl)
	}
	cir.KubeClient.CoreV1().ConfigMaps("openshift-console").Delete(context.TODO(), "console-config", metav1.DeleteOptions{})
	endpointaddr, endpointport, clusterurl = cir.getMasterAddresses()
	if len(endpointaddr) < 1 || len(endpointport) < 1 {
		t.Errorf("Failed to get clusterinfo. endpointaddr:%v, endpointport:%v, clusterurl:%v", endpointaddr, endpointport, clusterurl)
	}

	coreEndpointAddr, coreEndpointPort, err := cir.readAgentConfig()
	if err != nil {
		t.Errorf("Failed to read agent config. coreEndpoindAddr:%v, coreEndpointPort:%v, err:%v", coreEndpointAddr, coreEndpointPort, err)
	}

	err = cir.setEndpointAddressFromService(coreEndpointAddr, coreEndpointPort)
	if err != nil {
		t.Errorf("Failed to read agent config. coreEndpoindAddr:%v, coreEndpointPort:%v, err:%v", coreEndpointAddr, coreEndpointPort, err)
	}
	err = cir.setEndpointAddressFromRoute(coreEndpointAddr)
	if err == nil {
		t.Errorf("set endpoint should have error")
	}
	version := cir.getVersion()
	if version == "" {
		t.Errorf("Failed to get version")
	}
	_, err = cir.getNodeList()
	if err != nil {
		t.Errorf("Failed to get nodelist, err: %v", err)
	}
	_, _, err = cir.getDistributionInfoAndClusterID()
	if err != nil {
		t.Errorf("Failed to get distributeinfo, err: %v", err)
	}
}

const clusterVersions = `{
  "apiVersion": "config.openshift.io/v1",
  "kind": "ClusterVersion",
  "metadata": {
	"name": "version"
  },
  "spec": {
    "channel": "stable-4.5",
    "clusterID": "ffd989a0-8391-426d-98ac-86ae6d051433",
    "upstream": "https://api.openshift.com/api/upgrades_info/v1/graph"
  },
 "status": {
	"availableUpdates": [
	  {
		"force": false,
		"image": "quay.io/openshift-release-dev/ocp-release@sha256:95cfe9273aecb9a0070176210477491c347f8e69e41759063642edf8bb8aceb6",
                "version": "4.5.14"
	  },
	  {
		"force": false,
		"image": "quay.io/openshift-release-dev/ocp-release@sha256:adb5ef06c54ff75ca9033f222ac5e57f2fd82e49bdd84f737d460ae542c8af60",
		"version": "4.5.16"
	  },
	  {
		"force": false,
		"image": "quay.io/openshift-release-dev/ocp-release@sha256:8d104847fc2371a983f7cb01c7c0a3ab35b7381d6bf7ce355d9b32a08c0031f0",
		"version": "4.5.13"
	  },
	  {
		"force": false,
		"image": "quay.io/openshift-release-dev/ocp-release@sha256:6dde1b3ad6bec35364b2b89172cfea0459df75c99a4031f6f7b2a94eb9b166cf",
		"version": "4.5.17"
	  },
	  {
		"force": false,
		"image": "quay.io/openshift-release-dev/ocp-release@sha256:bae5510f19324d8e9c313aaba767e93c3a311902f5358fe2569e380544d9113e",
		"version": "4.5.19"
	  },
	  {
		"force": false,
		"image": "quay.io/openshift-release-dev/ocp-release@sha256:1df294ebe5b84f0eeceaa85b2162862c390143f5e84cda5acc22cc4529273c4c",
		"version": "4.5.15"
	  },
	  {
 	 	"force": false,
		"image": "quay.io/openshift-release-dev/ocp-release@sha256:72e3a1029884c70c584a0cadc00c36ee10764182425262fb23f77f32732ef366",
		"version": "4.5.18"
	  },
	  {
		"force": false,
		"image": "quay.io/openshift-release-dev/ocp-release@sha256:78b878986d2d0af6037d637aa63e7b6f80fc8f17d0f0d5b077ac6aca83f792a0",
		"version": "4.5.20"
	  }
	],
	"conditions": [
	  {
		"lastTransitionTime": "2020-09-30T09:00:07Z",
		"message": "Done applying 4.5.11",
		"status": "True",
		"type": "Available"
	  },
	  {
		"lastTransitionTime": "2020-09-30T08:45:02Z",
		"status": "False",
		"type": "Failing"
	  },
	  {
		"lastTransitionTime": "2020-09-30T09:00:07Z",
		"message": "Cluster version is 4.5.11",
		"status": "False",
		"type": "Progressing"
	  },
	  {
		"lastTransitionTime": "2020-10-06T13:50:43Z",
		"status": "True",
		"type": "RetrievedUpdates"
	  }
	],
	"desired": {
	  "force": false,
	  "image": "quay.io/openshift-release-dev/ocp-release@sha256:4d048ae1274d11c49f9b7e70713a072315431598b2ddbb512aee4027c422fe3e",
	  "version": "4.5.11"
	},
 	"history": [
	  {
	 	"completionTime": "2020-09-30T09:00:07Z",
		"image": "quay.io/openshift-release-dev/ocp-release@sha256:4d048ae1274d11c49f9b7e70713a072315431598b2ddbb512aee4027c422fe3e",
		"startedTime": "2020-09-30T08:36:46Z",
		"state": "Completed",
		"verified": false,
		"version": "4.5.11"
	  }
	],
	"observedGeneration": 1,
	"versionHash": "4lK_pl-YbSw="
  }
}`

func newClusterVersions(version string) *unstructured.Unstructured {
	if version == "3.x" {
		return &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "test/v1",
				"kind":       "test",
				"metadata": map[string]interface{}{
					"name": "test",
				},
			},
		}
	}
	if version == "4.x" {
		obj := unstructured.Unstructured{}
		obj.UnmarshalJSON([]byte(clusterVersions))
		return &obj
	}
	return nil
}

func TestClusterInfoReconciler_getOCPDistributionInfo(t *testing.T) {
	cir := NewClusterInfoReconciler()

	tests := []struct {
		name          string
		dynamicClient dynamic.Interface
		expectVersion string
		expectError   string
	}{
		{
			name:          "OCP4.x",
			dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), newClusterVersions("4.x")),
			expectVersion: "4.5.11",
			expectError:   "",
		},
		{
			name:          "OCP3.x",
			dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), newClusterVersions("3.x")),
			expectVersion: "3.x",
			expectError:   "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cir.ManagedClusterDynamicClient = test.dynamicClient
			info, _, err := cir.getOCPDistributionInfo()
			assert.Equal(t, info.Version, test.expectVersion)
			if err != nil {
				assert.Equal(t, err.Error(), test.expectError)
			}
		})
	}
}
