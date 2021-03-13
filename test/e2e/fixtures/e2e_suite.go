package fixtures

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/TwinProduction/go-color"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	// load authentication plugin for obtaining credentials from cloud providers.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"

	"github.com/argoproj/argo-workflows/v3/config"
	"github.com/argoproj/argo-workflows/v3/pkg/apis/workflow"
	"github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	"github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned/typed/workflow/v1alpha1"
	"github.com/argoproj/argo-workflows/v3/util/kubeconfig"
	"github.com/argoproj/argo-workflows/v3/workflow/common"
	"github.com/argoproj/argo-workflows/v3/workflow/hydrator"
)

const (
	Namespace      = "argo"
	Label          = workflow.WorkflowFullName + "/test" // mark this workflow as a test
	defaultTimeout = 30 * time.Second
)

type E2ESuite struct {
	suite.Suite
	Config            *config.Config
	Persistence       *Persistence
	RestConfig        *rest.Config
	wfClient          v1alpha1.WorkflowInterface
	wfebClient        v1alpha1.WorkflowEventBindingInterface
	wfTemplateClient  v1alpha1.WorkflowTemplateInterface
	cwfTemplateClient v1alpha1.ClusterWorkflowTemplateInterface
	cronClient        v1alpha1.CronWorkflowInterface
	KubeClient        kubernetes.Interface
	hydrator          hydrator.Interface
	testStartedAt     time.Time
	slowTests         []string
}

func (s *E2ESuite) SetupSuite() {
	var err error
	s.RestConfig, err = kubeconfig.DefaultRestConfig()
	s.CheckError(err)
	s.KubeClient, err = kubernetes.NewForConfig(s.RestConfig)
	s.CheckError(err)
	configController := config.NewController(Namespace, "workflow-controller-configmap", s.KubeClient, config.EmptyConfigFunc)

	ctx := context.Background()
	c, err := configController.Get(ctx)
	s.CheckError(err)
	s.Config = c.(*config.Config)
	s.wfClient = versioned.NewForConfigOrDie(s.RestConfig).ArgoprojV1alpha1().Workflows(Namespace)
	s.wfebClient = versioned.NewForConfigOrDie(s.RestConfig).ArgoprojV1alpha1().WorkflowEventBindings(Namespace)
	s.wfTemplateClient = versioned.NewForConfigOrDie(s.RestConfig).ArgoprojV1alpha1().WorkflowTemplates(Namespace)
	s.cronClient = versioned.NewForConfigOrDie(s.RestConfig).ArgoprojV1alpha1().CronWorkflows(Namespace)
	s.Persistence = newPersistence(s.KubeClient, s.Config)
	s.hydrator = hydrator.New(s.Persistence.offloadNodeStatusRepo)
	s.cwfTemplateClient = versioned.NewForConfigOrDie(s.RestConfig).ArgoprojV1alpha1().ClusterWorkflowTemplates()
}

func (s *E2ESuite) TearDownSuite() {
	s.Persistence.Close()
	for _, x := range s.slowTests {
		_, _ = fmt.Println(color.Ize(color.Yellow, fmt.Sprintf("=== SLOW TEST:  %s", x)))
	}
}

func (s *E2ESuite) BeforeTest(string, string) {
	s.DeleteResources()
	s.testStartedAt = time.Now()
}

func (s *E2ESuite) AfterTest(suiteName, testName string) {
	if s.T().Skipped() { // by default, we don't get good logging at test end
		_, _ = fmt.Println(color.Ize(color.Gray, "=== SKIP: "+suiteName+"/"+testName))
	} else if s.T().Failed() { // by default, we don't get good logging at test end
		_, _ = fmt.Println(color.Ize(color.Red, "=== FAIL: "+suiteName+"/"+testName))
		os.Exit(1)
	} else {
		_, _ = fmt.Println(color.Ize(color.Green, "=== PASS: "+suiteName+"/"+testName))
		took := time.Since(s.testStartedAt)
		if took > 20*time.Second {
			s.slowTests = append(s.slowTests, fmt.Sprintf("%s/%s took %v", suiteName, testName, took.Truncate(time.Second)))
		}
	}
}

func (s *E2ESuite) DeleteResources() {
	ctx := context.Background()
	// delete pods first, this means workflows can finish faster
	err := s.KubeClient.CoreV1().Pods(Namespace).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: common.LabelKeyWorkflow})
	s.CheckError(err)

	hasTestLabel := metav1.ListOptions{LabelSelector: Label}
	resources := []schema.GroupVersionResource{
		{Version: "v1", Resource: "resourcequotas"},
		{Group: workflow.Group, Version: workflow.Version, Resource: workflow.CronWorkflowPlural},
		{Group: workflow.Group, Version: workflow.Version, Resource: workflow.WorkflowPlural},
		{Group: workflow.Group, Version: workflow.Version, Resource: workflow.WorkflowTemplatePlural},
		{Group: workflow.Group, Version: workflow.Version, Resource: workflow.ClusterWorkflowTemplatePlural},
		{Group: workflow.Group, Version: workflow.Version, Resource: workflow.WorkflowEventBindingPlural},
		{Group: workflow.Group, Version: workflow.Version, Resource: "sensors"},
		{Group: workflow.Group, Version: workflow.Version, Resource: "eventsources"},
		{Version: "v1", Resource: "configmaps"},
	}
	for _, r := range resources {
		s.CheckError(s.dynamicFor(r).DeleteCollection(ctx, metav1.DeleteOptions{}, hasTestLabel))
	}

	// delete archived workflows from the archive
	if s.Persistence.IsEnabled() {
		archive := s.Persistence.workflowArchive
		parse, err := labels.ParseToRequirements(Label)
		s.CheckError(err)
		workflows, err := archive.ListWorkflows(Namespace, time.Time{}, time.Time{}, parse, 0, 0)
		s.CheckError(err)
		for _, w := range workflows {
			err := archive.DeleteWorkflow(string(w.UID))
			s.CheckError(err)
		}
	}
}

func (s *E2ESuite) Need(needs ...Need) {
	for _, n := range needs {
		met, message := n(s)
		if !met {
			s.T().Skip("unmet need: " + message)
		}
	}
}

func (s *E2ESuite) dynamicFor(r schema.GroupVersionResource) dynamic.ResourceInterface {
	resourceInterface := dynamic.NewForConfigOrDie(s.RestConfig).Resource(r)
	if r.Resource == workflow.ClusterWorkflowTemplatePlural {
		return resourceInterface
	}
	return resourceInterface.Namespace(Namespace)
}

func (s *E2ESuite) CheckError(err error) {
	s.T().Helper()
	if err != nil {
		s.T().Fatal(err)
	}
}

func (s *E2ESuite) GetBasicAuthToken() string {
	if s.RestConfig.Username == "" {
		return ""
	}
	auth := s.RestConfig.Username + ":" + s.RestConfig.Password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func (s *E2ESuite) GetServiceAccountToken() (string, error) {
	// create the clientset
	clientset, err := kubernetes.NewForConfig(s.RestConfig)
	if err != nil {
		return "", err
	}

	ctx := context.Background()
	secretList, err := clientset.CoreV1().Secrets("argo").List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, sec := range secretList.Items {
		if strings.HasPrefix(sec.Name, "argo-server-token") {
			return string(sec.Data["token"]), nil
		}
	}
	return "", nil
}

func (s *E2ESuite) Given() *Given {
	return &Given{
		t:                 s.T(),
		client:            s.wfClient,
		wfebClient:        s.wfebClient,
		wfTemplateClient:  s.wfTemplateClient,
		cwfTemplateClient: s.cwfTemplateClient,
		cronClient:        s.cronClient,
		hydrator:          s.hydrator,
		kubeClient:        s.KubeClient,
	}
}
