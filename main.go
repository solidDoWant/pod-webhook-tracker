package main

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gravitational/trace"
	sloghttp "github.com/samber/slog-http"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
)

func main() {
	rootCmd := buildRootCommand()

	if err := rootCmd.Execute(); err != nil {
		report := trace.DebugReport(err)
		// This isn't ideal but because the upstream library HTML escapes template chars,
		// they need to be "unescaped" for readability here. TODO replace this lib.
		report = html.UnescapeString(report)
		fmt.Fprintln(os.Stderr, report)
		os.Exit(1)
	}
}

func buildRootCommand() *cobra.Command {
	server := NewWebhookServer()

	cmd := &cobra.Command{
		Use:   "pod-webhook-tracker",
		Short: "A tool to track webhook calls via Kubernetes pods labels",
		Long:  "This tool allows you to add a label to Kubernetes pods upon webhook call. It can be used to track the number of active jobs in a pod by incrementing or decrementing a label value.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return server.run()
		},
	}

	server.configureFlags(cmd)
	server.configureLogger()

	return cmd
}

type k8sConfig struct {
	configOverrides    clientcmd.ConfigOverrides
	explicitConfigPath string
}

func NewK8sConfig() *k8sConfig {
	return &k8sConfig{}
}

// Get the cluster configurations from the following sources, in order of precedence:
// 1. A provided "kubeconfig" flag
// 2. The default kubeconfig file, at ~/.kube/config
// 3. The in-cluster kubeconfig, if running in a pod
func (k8sc *k8sConfig) getClientSet() (*kubernetes.Clientset, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.ExplicitPath = k8sc.explicitConfigPath

	var clientConfig clientcmd.ClientConfig
	if term.IsTerminal(int(os.Stdin.Fd())) {
		clientConfig = clientcmd.NewInteractiveDeferredLoadingClientConfig(loadingRules, &k8sc.configOverrides, os.Stdin)
	} else {
		clientConfig = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &k8sc.configOverrides)
	}

	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, trace.Wrap(err, "failed to build Kubernetes client config")
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, trace.Wrap(err, "failed to create Kubernetes clientset")
	}

	return clientset, nil
}

func (k8sc *k8sConfig) configureFlags(cmd *cobra.Command) {
	kubeFlags := pflag.NewFlagSet("kube", pflag.ExitOnError)

	kubeFlags.StringVar(&k8sc.explicitConfigPath, clientcmd.RecommendedConfigPathFlag, "", "Path to the kubeconfig file to use for CLI requests.")

	clientcmd.BindOverrideFlags(&k8sc.configOverrides, kubeFlags, clientcmd.RecommendedConfigOverrideFlags("kube-"))
	var flagNames []string
	kubeFlags.VisitAll(func(kubeFlag *pflag.Flag) {
		flagNames = append(flagNames, kubeFlag.Name)
	})

	cmd.Flags().AddFlagSet(kubeFlags)
}

type webhookServer struct {
	namespace     string
	labelSelector string
	kubeConfig    *k8sConfig
	address       string
	label         string
	removeOnZero  bool
	allowNegative bool
}

func NewWebhookServer() *webhookServer {
	return &webhookServer{
		kubeConfig: NewK8sConfig(),
	}
}

func (ws *webhookServer) configureFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&ws.namespace, "namespace", "default", "The Kubernetes namespace to use for CLI requests.")
	cmd.Flags().StringVar(&ws.labelSelector, "label-selector", "", "Label selector to use when querying for pods. This should be used to restrict access to specific pods.")
	cmd.Flags().StringVar(&ws.address, "address", "0.0.0.0:8080", "The address to listen on for incoming webhook requests.")
	cmd.Flags().StringVar(&ws.label, "label", "active-jobs", "The label to use for tracking webhook calls for a pod. Default is 'active-jobs'.")
	cmd.Flags().BoolVar(&ws.removeOnZero, "remove-on-zero", true, "If true, the label will be removed when its value reaches zero. If false, the label will be set to zero instead.")
	cmd.Flags().BoolVar(&ws.allowNegative, "allow-negative", false, "If true, the label value can be negative. If false, the label value will not go below zero.")
	ws.kubeConfig.configureFlags(cmd)
}

func (ws *webhookServer) configureLogger() {
	slog.SetDefault(slog.New(log.New(os.Stderr)))
}

func (ws *webhookServer) run() error {
	ctx, sigintCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigintCancel()

	clientset, err := ws.kubeConfig.getClientSet()
	if err != nil {
		return trace.Wrap(err, "failed to get Kubernetes client set")
	}

	router := http.NewServeMux()
	router.HandleFunc("/increment", ws.buildIncrementHandler(clientset))
	router.HandleFunc("/decrement", ws.buildDecrementHandler(clientset))

	handler := sloghttp.Recovery(router)
	handler = sloghttp.New(slog.Default().WithGroup("http"))(handler)

	server := &http.Server{
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
		Handler:  handler,
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
		Addr:     ws.address,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("Starting webhook server", "address", ws.address)
		serverErr <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = server.Shutdown(ctx)
	case err = <-serverErr:
	}

	return trace.Wrap(err, "webhook server failed")
}

func (ws *webhookServer) buildIncrementHandler(clientset *kubernetes.Clientset) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ws.updateLabelRequest(w, r, clientset, func(currentValue int) int {
			// Increment the label value
			return currentValue + 1
		})
	}
}

func (ws *webhookServer) buildDecrementHandler(clientset *kubernetes.Clientset) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ws.updateLabelRequest(w, r, clientset, func(currentValue int) int {
			if !ws.allowNegative && currentValue <= 0 {
				return 0
			}

			// Decrement the label value
			return currentValue - 1
		})
	}
}

func (ws *webhookServer) updateLabelRequest(w http.ResponseWriter, r *http.Request, clientset *kubernetes.Clientset, callback func(currentValue int) int) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	podName := r.URL.Query().Get("pod_name")
	if podName == "" {
		http.Error(w, "Missing pod_name query parameter", http.StatusBadRequest)
		return
	}

	_ = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		return ws.updateLabel(r.Context(), w, clientset, podName, callback)
	})
}

func (ws *webhookServer) updateLabel(ctx context.Context, w http.ResponseWriter, clientset *kubernetes.Clientset, podName string, callback func(currentValue int) int) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	pods, err := clientset.CoreV1().Pods(ws.namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", podName),
		LabelSelector: ws.labelSelector,
		Limit:         1,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list pods: %v", err), http.StatusInternalServerError)
		return trace.Wrap(err, "failed to list pods")
	}

	if len(pods.Items) == 0 {
		http.Error(w, "Pod not found", http.StatusNotFound)
		return fmt.Errorf("pod %s not found in namespace %s", podName, ws.namespace)
	}
	pod := &pods.Items[0]

	valueStr, ok := pod.Labels[ws.label]
	labelValue := 0
	if ok {
		labelValue, err = strconv.Atoi(valueStr)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid %q label value: %q", ws.label, valueStr), http.StatusInternalServerError)
			return trace.Wrap(err, "invalid %q label value: %q", ws.label, valueStr)
		}
	}

	newValue := callback(labelValue)
	if newValue == 0 && ws.removeOnZero {
		delete(pod.Labels, ws.label)
	} else {
		if pod.Labels == nil {
			pod.Labels = make(map[string]string, 1)
		}
		pod.Labels[ws.label] = strconv.Itoa(newValue)
	}

	_, err = clientset.CoreV1().Pods(pod.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
	if err != nil {
		http.Error(w, "Failed to update pod label", http.StatusInternalServerError)
		return trace.Wrap(err, "failed to update pod label")
	}

	fmt.Fprintf(w, "%d", newValue)
	return nil
}
