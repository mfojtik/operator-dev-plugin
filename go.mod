module github.com/mfojtik/operator-dev-plugin

go 1.13

require (
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	k8s.io/apimachinery v0.0.0-20191020214737-6c8691705fc5
	k8s.io/cli-runtime v0.0.0-20191023071533-6ea64d505988
	k8s.io/client-go v11.0.0+incompatible
)

replace k8s.io/client-go => k8s.io/client-go v0.0.0-20190918160344-1fbdaa4c8d90
