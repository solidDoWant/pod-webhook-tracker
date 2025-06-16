# pod-webhook-tracker

This is a simple program that tracks the number of webhook calls it receives by storing the call count in a Kubetnetes
pod label. This can be used in conjunction with pod disruption budgets to prevent pod eviction when the pod is
processing jobs. Upon job completion, another webhook call can be made to decrement the stored value. When the value
reaches 0, the label will be removed (configurable), causing pods to not be matched by a disruption budget.

## Setup

Run the program in a pod in the Kubernetes cluster. The pod's service account must have access to `list` and `update`
pods that are running jobs.

Upon job start, the job should send a HTTP POST request to `http://pod-webhook-tracker/increment?pod_name=$(POD_NAME)`,
replacing `$(POD_NAME)` with the name of the pod that is making the HTTP Request. Upon job completion (regardless of
success or failure), the pod should make a HTTP POST request to
`http://pod-webhook-tracker/decrement?pod_name=$(POD_NAME)`.

An example deployment for use with [FileFlows](https://fileflow.com) is available
[here](https://github.com/solidDoWant/infra-mk3/tree/b5a65f55459f0b5725766e4c879fc6210732330c/cluster/gitops/media/fileflows/job-tracker).

## Usage

```console
This tool allows you to add a label to Kubernetes pods upon webhook call. It can be used to track the number of active jobs in a pod by incrementing or decrementing a label value.

Usage:
  pod-webhook-tracker [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  serve       Run the webhook server
  version     Print the version number

Flags:
  -h, --help   help for pod-webhook-tracker

Use "pod-webhook-tracker [command] --help" for more information about a command.
```

### Serve subcommand

```console
Run the webhook server

Usage:
  pod-webhook-tracker serve [flags]

Flags:
      --address string                      The address to listen on for incoming webhook requests. (default "0.0.0.0:8080")
      --allow-negative                      If true, the label value can be negative. If false, the label value will not go below zero.
  -h, --help                                help for serve
      --kube-as string                      Username to impersonate for the operation
      --kube-as-group stringArray           Group to impersonate for the operation, this flag can be repeated to specify multiple groups.
      --kube-as-uid string                  UID to impersonate for the operation
      --kube-certificate-authority string   Path to a cert file for the certificate authority
      --kube-client-certificate string      Path to a client certificate file for TLS
      --kube-client-key string              Path to a client key file for TLS
      --kube-cluster string                 The name of the kubeconfig cluster to use
      --kube-context string                 The name of the kubeconfig context to use
      --kube-disable-compression            If true, opt-out of response compression for all requests to the server
      --kube-insecure-skip-tls-verify       If true, the server's certificate will not be checked for validity. This will make your HTTPS connections insecure
  -n, --kube-namespace string               If present, the namespace scope for this CLI request
      --kube-password string                Password for basic authentication to the API server
      --kube-proxy-url string               If provided, this URL will be used to connect via proxy
      --kube-request-timeout string         The length of time to wait before giving up on a single server request. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h). A value of zero means don't timeout requests. (default "0")
      --kube-server string                  The address and port of the Kubernetes API server
      --kube-tls-server-name string         If provided, this name will be used to validate server certificate. If this is not provided, hostname used to contact the server is used.
      --kube-token string                   Bearer token for authentication to the API server
      --kube-user string                    The name of the kubeconfig user to use
      --kube-username string                Username for basic authentication to the API server
      --kubeconfig string                   Path to the kubeconfig file to use for CLI requests.
      --label string                        The label to use for tracking webhook calls for a pod. Default is 'active-jobs'. (default "active-jobs")
      --label-selector string               Label selector to use when querying for pods. This should be used to restrict access to specific pods.
      --namespace string                    The Kubernetes namespace to use for CLI requests. (default "default")
      --remove-on-zero                      If true, the label will be removed when its value reaches zero. If false, the label will be set to zero instead. (default true)
```

## Security

The `--label-selector` argument should be used to limit which pods the program will affect. If the program receives a
request for a pod that does not match this selector, the HTTP call will return a 404 response.

Not authentication, authorization, or encryption is built-in. Users are expected to secure access to this program via
external mechanisms, such as [network
policies](https://kubernetes.io/docs/concepts/services-networking/network-policies/),
[kube-rbac-proxy](https://github.com/brancz/kube-rbac-proxy), or a [service mesh](https://istio.io/).

## Building

The follow makefile targets are available:

* `binary` - build a binary for the local machine
* `build` - build all local assets (binary, tarball, container image)
* `build-all` - build all assets for all targeted machines (amd64, arm64)
* `release` - build and release all assets (be sure to set `VERSION=1.2.3-some.semver.version`)
* `clean` - remove all built assets
