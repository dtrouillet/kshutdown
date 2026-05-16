//go:build integration

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

// Package webhook contains the integration test suite for the admission webhook.
// Run with: go test -tags=integration ./internal/webhook/...
package webhook

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	kshutdowniov1alpha1 "github.com/dtrouillet/kshutdown/api/v1alpha1"
)

var (
	integCtx    context.Context
	integCancel context.CancelFunc
	integEnv    *envtest.Environment
	integCfg    *rest.Config
	integClient client.Client

	// allowedUsers drives which usernames the injected SAR checker grants access to.
	allowedUsers map[string]bool
)

// integCheckPermission is injected into ShutdownGroupValidator for integration tests.
// It grants access to usernames present in allowedUsers.
func integCheckPermission(_ context.Context, _ client.Client,
	user string, _ []string,
	_, _, _, _, _ string,
) (bool, error) {
	return allowedUsers[user], nil
}

func TestWebhooksIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Webhook Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	integCtx, integCancel = context.WithCancel(context.TODO())

	var err error
	err = kshutdowniov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	integEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "config", "webhook")},
		},
	}
	if dir := integFindBinDir(); dir != "" {
		integEnv.BinaryAssetsDirectory = dir
	}

	integCfg, err = integEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	integClient, err = client.New(integCfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	wio := integEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(integCfg, ctrl.Options{
		Scheme: scheme.Scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    wio.LocalServingHost,
			Port:    wio.LocalServingPort,
			CertDir: wio.LocalServingCertDir,
		}),
		LeaderElection:         false,
		HealthProbeBindAddress: "0",
		Metrics:                metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&ShutdownGroupValidator{
		Client:          mgr.GetClient(),
		checkPermission: integCheckPermission,
	}).SetupWebhookWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(integCtx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	integCancel()
	Expect(integEnv.Stop()).To(Succeed())
})

func integFindBinDir() string {
	base := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(base, e.Name())
		}
	}
	return ""
}

// impersonatingClient returns a client that impersonates the given username.
func impersonatingClient(username string) (client.Client, error) {
	cfg := rest.CopyConfig(integCfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: username}
	s := runtime.NewScheme()
	_ = kshutdowniov1alpha1.AddToScheme(s)
	return client.New(cfg, client.Options{Scheme: s})
}
