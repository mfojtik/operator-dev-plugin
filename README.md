### operator-dev-plugin

This is a [kubectl plugin](https://kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/) that extends the the OpenShift CLI command with the
`operator-dev` sub-command. This sub-command can perform various tasks on operators that operator developers need to do in order to test their
changes.

#### Installation



#### Usage

The `override` command is used when developer want to override the operator container image with custom built image, typically for testing purposes.
The command will edit `clusterversion/version` object and set the right override for the operator deployment. The it will update the operator deployment
and set the desired image for it.

```shell script
$ oc operator-dev override kube-apiserver --image=docker.io/mfojtik/custom-image:debug
```

In case developer want to revert this change and make cluster version operator manage the operator again:

```shell script
$ oc operator-dev override kube-apiserver --managed
```
