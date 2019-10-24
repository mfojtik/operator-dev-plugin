package override

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// OverrideOptions provides information required to update
// the current context on a user's KUBECONFIG
type OverrideOptions struct {
	configFlags *genericclioptions.ConfigFlags

	args    []string
	image   string
	managed bool

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
	# override will tell cluster version operator to stop managing given operator and replace its image with custom image
	# the 'kube-apiserver' must be valid cluster operator name (oc get clusteroperators).
	%[1]s kube-apiserver --image=docker.io/foo/apiserver:debug

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
	cmd.Flags().BoolVar(&o.managed, "managed", false, "set to true if you want cluster version operator to manage this operator")
	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

// getOperatorNamespace guess the namespace where the operator is being deployed.
// TODO: This should not be necessary and we should have this information as related object in clusteroperator/foo
func getOperatorNamespace(operatorName string) string {
	operatorNamespace := strings.TrimPrefix(operatorName, "openshift-")
	return "openshift-" + operatorNamespace + "-operator"
}

// getOperatorDeploymentName guess the deployment name of the operator.
// TODO: This should not be necessary and we should have this information as related object in clusteroperator/foo
func getOperatorDeploymentName(operatorName string) string {
	return operatorName + "-operator"
}

// makeJSONPatch construct the JSON patch string used to patch the clusterversion/version object
func makeJSONPatch(operatorName string, managed bool) []byte {
	unmanagedString := "true"
	if managed {
		unmanagedString = "false"
	}
	return []byte(fmt.Sprintf(`{"spec":{"overrides":[{"group":"apps/v1","kind":"Deployment","name":"%s","namespace":"%s","unmanaged":%s}]}}`,
		getOperatorNamespace(operatorName), getOperatorDeploymentName(operatorName), unmanagedString))
}

func (o *OverrideOptions) Validate() error {
	if len(o.args) == 0 {
		return fmt.Errorf("clusteroperator/name must be specified")
	}
	if len(o.image) == 0 && !o.managed {
		return fmt.Errorf("image must be specified")
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
	if _, err := o.dynamicClient.Resource(clusterOperatorGvr).Get(o.args[0], metav1.GetOptions{}); err != nil {
		return fmt.Errorf("operator %q is not valid operator: %v", o.args[0], err)
	}

	if _, err := o.dynamicClient.Resource(clusterVersionGvr).Patch("version", types.MergePatchType, makeJSONPatch(o.args[0], o.managed), metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("failed to patch clusterversion/version: %v", err)
	}

	// if --managed is used, patch the clusterversion to unmanaged: false and exit
	if o.managed {
		o.printOut("-> Operator %q now managed ...\n", getOperatorDeploymentName(o.args[0]))
		return nil
	}

	o.printOut("-> Operator %q is not managed ...\n", getOperatorDeploymentName(o.args[0]))

	// In some case CVO will take time to reconcile new config, so give it 1s for starter
	// TODO: The ClusterVersion operator should really reflect the current state in it's status
	time.Sleep(1 * time.Second)

	// update the operator deployment with provided image
	// TODO: verify the operator image was really changed
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		operatorDeployment, err := o.kubeClient.AppsV1().Deployments(getOperatorNamespace(o.args[0])).Get(getOperatorDeploymentName(o.args[0]), metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to get deployment: %v", err)
		}
		for i := range operatorDeployment.Spec.Template.Spec.Containers {
			operatorDeployment.Spec.Template.Spec.Containers[i].Image = o.image
		}
		for i := range operatorDeployment.Spec.Template.Spec.InitContainers {
			operatorDeployment.Spec.Template.Spec.Containers[i].Image = o.image
		}
		_, err = o.kubeClient.AppsV1().Deployments(getOperatorNamespace(o.args[0])).Update(operatorDeployment)
		return err
	}); err != nil {
		return err
	}

	o.printOut("-> Operator %q image is now %q  ...\n", getOperatorDeploymentName(o.args[0]), o.image)

	return nil
}
