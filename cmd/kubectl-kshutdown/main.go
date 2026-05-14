/*
Copyright 2026.

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

package main

import (
	"os"

	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kshutdownv1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

var (
	scheme = runtime.NewScheme()

	kubeconfig string
	namespace  string

	// clientFactory is called by each sub-command to obtain the two clients it needs.
	clientFactory func() (client.Client, *kubernetes.Clientset, error)
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kshutdownv1alpha1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(batchv1.AddToScheme(scheme))
}

func newClients() (client.Client, *kubernetes.Clientset, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules,
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, nil, err
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, nil, err
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}

	return c, cs, nil
}

func main() {
	clientFactory = newClients

	root := &cobra.Command{
		Use:   "kubectl-kshutdown",
		Short: "Manage functional shutdown groups on Kubernetes",
	}

	root.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "",
		"Path to kubeconfig (defaults to KUBECONFIG env / ~/.kube/config)")
	root.PersistentFlags().StringVarP(&namespace, "namespace", "n", "", "Namespace of the ShutdownGroup")

	root.AddCommand(
		newDownCmd(),
		newUpCmd(),
		newStatusCmd(),
		newListCmd(),
		newHistoryCmd(),
		newDefineCmd(),
		newExportCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
