package override

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// OverrideOptions provides information required to update
// the current context on a user's KUBECONFIG
type OverrideOptions struct {
	configFlags *genericclioptions.ConfigFlags

	args       []string
	image      string
	operand    string
	deployment string
	verbosity  string
	managed    bool

	dynamicClient dynamic.Interface
	kubeClient    kubernetes.Interface

	genericclioptions.IOStreams
}

// NewOverrideOptions provides an instance of OverrideOptions with default values
func NewOverrideOptions(streams genericclioptions.IOStreams) *OverrideOptions {
	return &OverrideOptions{
		configFlags: genericclioptions.NewConfigFlags(true),

		IOStreams: streams,
	}
}

var (
	operatorOverrideExample = `
	# override will tell cluster version operator to stop managing given operator and
    # - (optionally) replace its operator image
    # - (optionally) replace its operand image.
    # The 'kube-apiserver' must be valid cluster operator name (oc get clusteroperators).
	%[1]s kube-apiserver --image=docker.io/foo/apiserver-operator:debug --operand-image docker.io/foo/apiserver:debug

    # will make the openshift apiserver operator managed again
	%[1]s openshift-apiserver --managed
`
)

func NewCmdOperatorReplace(streams genericclioptions.IOStreams) *cobra.Command {
	o := NewOverrideOptions(streams)

	cmd := &cobra.Command{
		Use:     "override <clusteroperator/name>",
		Short:   "Override the target operator image",
		Example: fmt.Sprintf(operatorOverrideExample, "oc operator-dev override"),
		RunE: func(c *cobra.Command, args []string) error {
			o.args = args
			if err := o.Validate(); err != nil {
				return err
			}
			if err := o.Complete(); err != nil {
				return err
			}
			return o.Run()
		},
	}

	cmd.Flags().StringVar(&o.image, "image", o.image, "image to use for given operator")
	cmd.Flags().StringVar(&o.operand, "operand-image", o.operand, "image to use for given operator's operand (only supports those with IMAGE environment variable in the operator deployment)")
	cmd.Flags().StringVar(&o.verbosity, "verbosity", o.verbosity, "set the verbosity level for operator")
	cmd.Flags().BoolVar(&o.managed, "managed", false, "set to true if you want cluster version operator to manage this operator")
	cmd.Flags().StringVar(&o.deployment, "deployment", o.deployment, "custom deployment name")
	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// getOperatorNamespace guess the namespace where the operator is being deployed.
// TODO: This should not be necessary and we should have this information as related object in clusteroperator/foo
func getOperatorNamespace(operatorName string) string {
	operatorNamespace := strings.TrimPrefix(operatorName, "openshift-")
	switch operatorName {
	case "insights":
		return "openshift-insights"
	case "openshift-apiserver":
		return "openshift-apiserver-operator"
	default:
		return "openshift-" + operatorNamespace + "-operator"
	}
}

// getOperatorDeploymentName guess the deployment name of the operator.
// TODO: This should not be necessary and we should have this information as related object in clusteroperator/foo
func getOperatorDeploymentName(operatorName string) string {
	return operatorName + "-operator"
}

func (o *OverrideOptions) Validate() error {
	if len(o.args) == 0 {
		return fmt.Errorf("clusteroperator/name must be specified")
	}
	if len(o.image) != 0 && o.managed {
		return fmt.Errorf("image must be empty when operator is managed")
	}
	return nil
}

func (o *OverrideOptions) printOut(message string, objs ...interface{}) {
	if _, err := fmt.Fprintf(o.Out, message, objs...); err != nil {
		panic(err)
	}
}

func (o *OverrideOptions) Complete() error {
	restConfig, err := o.configFlags.ToRESTConfig()
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	o.dynamicClient = dynamicClient

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	o.kubeClient = kubeClient

	return nil
}

func (o *OverrideOptions) Run() error {
	// TODO: this should really come from discovery, but lets be lazy
	clusterOperatorGvr := schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "clusteroperators"}
	clusterVersionGvr := schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "clusterversions"}

	// check if the cluster operator name is a valid operator
	_, err := o.dynamicClient.Resource(clusterOperatorGvr).Get(o.args[0], metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("operator %q is not valid operator: %v", o.args[0], err)
	}

	// sanity check for existence of the deployment
	deploymentName := getOperatorDeploymentName(o.args[0])
	if len(o.deployment) > 0 {
		deploymentName = o.deployment
	}
	deploymentNS := getOperatorNamespace(o.args[0])
	if _, err := o.kubeClient.AppsV1().Deployments(deploymentNS).Get(deploymentName, metav1.GetOptions{}); errors.IsNotFound(err) {
		deployments, err := o.kubeClient.AppsV1().Deployments(deploymentNS).List(metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployments in namespace %s: %v", deploymentNS, err)
		}
		if len(deployments.Items) == 1 {
			deploymentName = deployments.Items[0].Name
		} else {
			return fmt.Errorf("deployment %s/%s not found. Maybe try --deployment for a custom name", deploymentNS, deploymentName)
		}
	} else if err != nil {
		return fmt.Errorf("unable to get deployment  %s/%s: %v", deploymentNS, deploymentName, err)
	}

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		version, err := o.dynamicClient.Resource(clusterVersionGvr).Get("version", metav1.GetOptions{})
		if err != nil {
			return err
		}

		// replace or append override
		overrides, _, err := unstructured.NestedSlice(version.Object, "spec", "overrides")
		found := false
		for _, x := range overrides {
			override, ok := x.(map[string]interface{})
			if !ok {
				continue // ignore
			}

			kind, _, _ := unstructured.NestedString(override, "kind")
			group, _, _ := unstructured.NestedString(override, "group")
			ns, _, _ := unstructured.NestedString(override, "namespace")
			name, _, _ := unstructured.NestedString(override, "name")

			if kind == "Deployment" && group == "apps/v1" && ns == deploymentNS && name == deploymentName {
				found = true
				unstructured.SetNestedField(override, !o.managed, "unmanaged")
				break
			}
		}
		if !found {
			overrides = append(overrides, map[string]interface{}{
				"group":     "apps/v1",
				"kind":      "Deployment",
				"namespace": deploymentNS,
				"name":      deploymentName,
				"unmanaged": !o.managed,
			})
			unstructured.SetNestedField(version.Object, overrides, "spec", "overrides")
		}

		_, err = o.dynamicClient.Resource(clusterVersionGvr).Update(version, metav1.UpdateOptions{})
		return err
	}); err != nil {
		return fmt.Errorf("failed to patch clusterversion/version: %v", err)
	}

	// if --managed is used, patch the clusterversion to unmanaged: false and exit
	if o.managed {
		o.printOut("-> Operator %q now managed ...\n", deploymentName)
		return nil
	}

	o.printOut("-> Operator %q is not managed ...\n", deploymentName)

	// In some case CVO will take time to reconcile new config, so give it 1s for starter
	// TODO: The ClusterVersion operator should really reflect the current state in it's status
	time.Sleep(1 * time.Second)

	// update the operator deployment with provided image
	// TODO: verify the operator image was really changed
	operandUpdated := false
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		operatorDeployment, err := o.kubeClient.AppsV1().Deployments(deploymentNS).Get(deploymentName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to get deployment: %v", err)
		}
		for i := range operatorDeployment.Spec.Template.Spec.Containers {
			if len(o.image) > 0 {
				operatorDeployment.Spec.Template.Spec.Containers[i].Image = o.image
			}

			if len(o.verbosity) > 0 {
				operatorDeployment.Spec.Template.Spec.Containers[i].Args = append(operatorDeployment.Spec.Template.Spec.Containers[i].Args, fmt.Sprintf("-v=%s", o.verbosity))
			}

			for j, ev := range operatorDeployment.Spec.Template.Spec.Containers[i].Env {
				if ev.Name == "OPERATOR_IMAGE" && len(o.image) > 0 {
					operatorDeployment.Spec.Template.Spec.Containers[i].Env[j].Value = o.image
				}
			}

			for j, ev := range operatorDeployment.Spec.Template.Spec.Containers[i].Env {
				if ev.Name == "IMAGE" && len(o.operand) > 0 {
					operandUpdated = true
					operatorDeployment.Spec.Template.Spec.Containers[i].Env[j].Value = o.operand
				}
			}
		}
		for i := range operatorDeployment.Spec.Template.Spec.InitContainers {
			operatorDeployment.Spec.Template.Spec.Containers[i].Image = o.image
		}
		_, err = o.kubeClient.AppsV1().Deployments(deploymentNS).Update(operatorDeployment)
		return err
	}); err != nil {
		return err
	}

	if len(o.image) > 0 {
		o.printOut("-> Operator %q image is now %q  ...\n", deploymentName, o.image)
	}
	if len(o.operand) > 0 {
		if operandUpdated {
			o.printOut("-> Operand image is now %q  ...\n", o.operand)
		} else {
			return fmt.Errorf("no IMAGE env var found in the deployment")
		}
	}

	return nil
}
