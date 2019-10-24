package override

import (
	"fmt"
	"log"
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
			return o.Run()
		},
	}

	cmd.Flags().StringVar(&o.image, "image", o.image, "image to use for given operator")
	cmd.Flags().BoolVar(&o.managed, "managed", false, "set to true if you want cluster version operator to manage this operator")
	o.configFlags.AddFlags(cmd.Flags())

	return cmd
}

func getOperatorNamespace(operatorName string) string {
	operatorNamespace := strings.TrimPrefix(operatorName, "openshift-")
	return "openshift-" + operatorNamespace + "-operator"
}

func getOperatorDeploymentName(operatorName string) string {
	return operatorName + "-operator"
}

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

func (o *OverrideOptions) Run() error {
	restConfig, err := o.configFlags.ToRESTConfig()
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	// check if the cluster operator name is a valid operator
	if _, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "clusteroperators",
	}).Get(o.args[0], metav1.GetOptions{}); err != nil {
		return err
	}

	if _, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "clusterversions",
	}).Patch("version", types.MergePatchType, makeJSONPatch(o.args[0], o.managed), metav1.PatchOptions{}); err != nil {
		return err
	}

	if o.managed {
		o.printOut("-> Operator %q now managed ...\n", getOperatorNamespace(o.args[0])+"/"+getOperatorDeploymentName(o.args[0]))
		return nil
	}

	o.printOut("-> Operator %q is not managed ...\n", getOperatorNamespace(o.args[0])+"/"+getOperatorDeploymentName(o.args[0]))

	// In some case CVO will take time to reconcile new config, so give it 1s for starter
	// TODO: The ClusterVersion operator should really reflect the current state in it's status
	time.Sleep(1 * time.Second)

	kubeClient := kubernetes.NewForConfigOrDie(restConfig)
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		operatorDeployment, err := kubeClient.AppsV1().Deployments(getOperatorNamespace(o.args[0])).Get(getOperatorDeploymentName(o.args[0]), metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to get deployment: %v", err)
		}
		for i := range operatorDeployment.Spec.Template.Spec.Containers {
			operatorDeployment.Spec.Template.Spec.Containers[i].Image = o.image
		}
		for i := range operatorDeployment.Spec.Template.Spec.InitContainers {
			operatorDeployment.Spec.Template.Spec.Containers[i].Image = o.image
		}
		log.Printf("containers: %#+v", operatorDeployment.Spec.Template.Spec.Containers)
		_, err = kubeClient.AppsV1().Deployments(getOperatorNamespace(o.args[0])).Update(operatorDeployment)
		return err
	}); err != nil {
		return err
	}

	o.printOut("-> Operator %q image changed to %q  ...\n", getOperatorNamespace(o.args[0])+"/"+getOperatorDeploymentName(o.args[0]), o.image)

	return nil
}
