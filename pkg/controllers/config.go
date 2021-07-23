package controllers

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"

	argocdClientSet "github.com/argoproj/argo-cd/v2/pkg/apiclient"
	"github.com/ghodss/yaml"
	"github.com/kballard/go-shellquote"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	//AppSource configmap name
	appSourceCM = "argocd-appsource-cm"
)

var (
	flags map[string]string
)

// getFlag returns flags[key] or fallback string if key
// does not exist
func getFlag(key, fallback string) string {
	val, ok := flags[key]
	if ok {
		return val
	}
	return fallback
}

// getBoolFlag returns flags[key] boolean or false if key
// does not exist
func getBoolFlag(key string) bool {
	return getFlag(key, "false") == "true"
}

// loadFlags populates the flags map with any keys and
// values found in the clientOpts string
func loadFlags(clientOpts string) (err error) {
	opts, err := shellquote.Split(clientOpts)
	if err != nil {
		return err
	}
	flags = make(map[string]string)
	var key string
	for _, opt := range opts {
		if strings.HasPrefix(opt, "--") {
			if key != "" {
				flags[key] = "true"
			}
			key = strings.TrimPrefix(opt, "--")
		} else if key != "" {
			flags[key] = opt
			key = ""
		} else {
			return errors.New("clientOpts invalid at '" + opt + "'")
		}
	}
	if key != "" {
		flags[key] = "true"
	}

	return nil
}

func (r *AppSourceReconciler) UpsertAppSourceConfig() (ok bool, err error) {
	if err := r.UpsertAppSourceConfigmap(); err != nil {
		return true, err
	}
	if err := r.UpsertProjectTemplate(); err != nil {
		return false, err
	}
	if err := r.UpsertArgoCDClients(); err != nil {
		return false, err
	}
	r.UpsertCompilers()
	return true, nil
}

// GetClientOpts loads all the flags found in the AppSource configmap
// and returns a ArgoCD ClientOpts object with any fields found
func (r *AppSourceReconciler) GetClientOpts() (*argocdClientSet.ClientOptions, error) {
	err := loadFlags(r.ConfigMap.Data["argocd.clientOpts"])
	if err != nil {
		return nil, err
	}

	token := os.Getenv("ARGOCD_TOKEN")

	return &argocdClientSet.ClientOptions{
		ServerAddr:        r.ConfigMap.Data["argocd.address"],
		AuthToken:         token,
		PlainText:         getBoolFlag("plaintext"),
		Insecure:          getBoolFlag("insecure"),
		CertFile:          getFlag("server-crt", ""),
		ClientCertFile:    getFlag("client-crt", ""),
		ClientCertKeyFile: getFlag("client-crt-key", ""),
		GRPCWeb:           getBoolFlag("grpc-web"),
		GRPCWebRootPath:   getFlag("grpc-web-root-path", ""),
		PortForward:       getBoolFlag("port-forward"),
		//? How should headers be handled?
		PortForwardNamespace: getFlag("port-forward-namespace", ""),
	}, nil
}

//GetAppSourceConfigmapOrDie returns the AppSource ConfigMap defined by admins or crashes with error
func (r *AppSourceReconciler) UpsertAppSourceConfigmap() (err error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig
	overrides := clientcmd.ConfigOverrides{}
	clientConfig := clientcmd.NewInteractiveDeferredLoadingClientConfig(loadingRules, &overrides, os.Stdin)
	config, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	//Get AppSource ConfigMap
	r.ConfigMap, err = clientset.CoreV1().ConfigMaps("argocd").Get(context.TODO(), appSourceCM, metav1.GetOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (r *AppSourceReconciler) UpsertArgoCDClients() error {
	argocdClientOpts, err := r.GetClientOpts()
	if err != nil {
		return err
	}
	argocdClient, err := argocdClientSet.NewClient(argocdClientOpts)
	if err != nil {
		return err
	}

	r.Clients.Applications.Closer, r.Clients.Applications.Client = argocdClient.NewApplicationClientOrDie()
	// if err != nil {
	// 	return err
	// }
	r.Clients.Projects.Closer, r.Clients.Projects.Client = argocdClient.NewProjectClientOrDie()
	// if err != nil {
	// 	return err
	// }
	return nil
}

func (r *AppSourceReconciler) UpsertProjectTemplate() error {
	appsourceProjectTemplate := ProjectTemplate{}
	err := yaml.Unmarshal([]byte(r.ConfigMap.Data["project.template"]), &appsourceProjectTemplate)
	if err != nil {
		return err
	}
	r.Project = appsourceProjectTemplate
	return nil
}

func (r *AppSourceReconciler) UpsertCompilers() {
	if r.Project.NamePattern != "" {
		r.Compilers.Pattern = regexp.MustCompile(r.Project.NamePattern)
	}
}
