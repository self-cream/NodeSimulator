module github.com/NJUPT-ISL/NodeSimulator

go 1.13

require (
	//github.com/self-cream/SCV v0.0.0-20201213061001-6c337bbac7a5
	github.com/NJUPT-ISL/SCV v0.0.0-20200908005541-d990930d5755
	github.com/go-logr/logr v0.3.0
	k8s.io/api v0.20.0
	k8s.io/apimachinery v0.20.0
	k8s.io/client-go v0.20.0
	k8s.io/klog v0.4.0
	sigs.k8s.io/controller-runtime v0.7.0
)

replace (
	github.com/NJUPT-ISL/SCV => github.com/self-cream/SCV v0.0.0-20201221024418-47401c0d23b4 //indirect
	k8s.io/client-go => k8s.io/client-go v0.20.0
)
