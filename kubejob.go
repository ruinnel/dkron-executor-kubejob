package main

import (
	"fmt"
	"github.com/armon/circbuf"
	"github.com/distribworks/dkron/v2/plugin"
	"github.com/distribworks/dkron/v2/plugin/types"
	"io"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

const (
	jobNameLabel = "dkron-job-name"
	maxBufSize   = 256000
	minCheckTerm = 5
)

// auto retry off
var defaultBackoffLimit int32 = 0
var defaultLabel = labels.Set{
	"app":      "dkron-kubejob",
	"heritage": "dkron",
}

type Config struct {
	Namespace                  string
	Job                        *batchv1.Job
	Timeout                    int64
	SuccessfulJobsHistoryLimit int
	FailedJobsHistoryLimit     int
	Debug                      bool
}

func (c *Config) checkTerm() int64 {
	term := c.Timeout / 10
	if term < minCheckTerm {
		term = minCheckTerm
	}
	return term
}

type KubeJob struct {
	client *kubernetes.Clientset
}

func makeResponse(output *circbuf.Buffer, err error) *types.ExecuteResponse {
	var out []byte
	var errorMessage string
	if output != nil {
		out = output.Bytes()
	}
	if err != nil {
		errorMessage = err.Error()
	}

	return &types.ExecuteResponse{Output: out, Error: errorMessage}
}

// prevent job name duplicate
func makeJobName(job *batchv1.Job) string {
	uid := strings.Split(string(uuid.NewUUID()), "-")
	suffix := uid[0]
	timestamp := time.Now().Unix()
	return fmt.Sprintf("%s-%d-%s", job.Name, timestamp, suffix)
}

func makePodListOptions(job *batchv1.Job) metav1.ListOptions {
	labelSet := labels.Set{
		"job-name":       job.Name,
		"controller-uid": string(job.UID),
	}
	return metav1.ListOptions{LabelSelector: labelSet.AsSelector().String()}
}

func makeLabels(dkronJobName string, job *batchv1.Job) labels.Set {
	var label map[string]string
	if job != nil && job.Labels != nil {
		label = job.Labels
	} else {
		label = labels.Set{}
	}
	label[jobNameLabel] = dkronJobName
	for key, val := range defaultLabel {
		label[key] = val
	}

	return label
}

func parseConfig(args *types.ExecuteRequest) (*Config, error) {
	config := &Config{}
	// namespace
	namespace, exists := args.Config["namespace"]
	if !exists {
		return nil, errors.NewBadRequest("namespace config not exists")
	}
	config.Namespace = namespace

	// job
	jobConfig, exists := args.Config["job"]
	if !exists {
		return nil, errors.NewBadRequest("job config not exists")
	}
	object, kind, err := scheme.Codecs.UniversalDeserializer().Decode([]byte(jobConfig), nil, nil)
	if err != nil {
		return nil, err
	}
	if kind.Group == "batch" && kind.Kind != "Job" {
		err = errors.NewBadRequest("kind is not a 'job'")
		return nil, err
	}

	config.Job = object.(*batchv1.Job)
	config.Job.Labels = makeLabels(args.JobName, config.Job)
	config.Job.Name = makeJobName(config.Job)
	if config.Job.Spec.BackoffLimit == nil {
		config.Job.Spec.BackoffLimit = &defaultBackoffLimit
	}

	// timeout
	timeoutConfig, exists := args.Config["timeout"]
	if !exists {
		return nil, errors.NewBadRequest("timeout config not exists")
	}
	timeout, err := strconv.ParseInt(timeoutConfig, 10, 64)
	if err != nil {
		return nil, errors.NewBadRequest("timeout config is not a number")
	}
	config.Timeout = timeout

	// successfulJobsHistoryLimit
	successfulJobsHistoryLimitConfig, exists := args.Config["successfulJobsHistoryLimit"]
	if !exists {
		return nil, errors.NewBadRequest("successfulJobsHistoryLimit config not exists")
	}
	successfulJobsHistoryLimit, err := strconv.ParseInt(successfulJobsHistoryLimitConfig, 10, 64)
	if err != nil {
		return nil, errors.NewBadRequest("successfulJobsHistoryLimit config is not a number")
	}
	config.SuccessfulJobsHistoryLimit = int(successfulJobsHistoryLimit)

	// failedJobsHistoryLimit
	failedJobsHistoryLimitConfig, exists := args.Config["failedJobsHistoryLimit"]
	if !exists {
		return nil, errors.NewBadRequest("failedJobsHistoryLimit config not exists")
	}
	failedJobsHistoryLimit, err := strconv.ParseInt(failedJobsHistoryLimitConfig, 10, 64)
	if err != nil {
		return nil, errors.NewBadRequest("failedJobsHistoryLimit config is not a number")
	}
	config.FailedJobsHistoryLimit = int(failedJobsHistoryLimit)

	return config, nil
}

func (pe *KubeJob) log(buffer *circbuf.Buffer, args ...interface{}) {
	log := fmt.Sprint(args...)
	if strings.HasSuffix(log, "\n") {
		buffer.Write([]byte(log))
	} else {
		buffer.Write([]byte(fmt.Sprintf("%s\n", log)))
	}
}

func (pe *KubeJob) logf(buffer *circbuf.Buffer, format string, args ...interface{}) {
	pe.log(buffer, fmt.Sprintf(format, args...))
}

func (pe *KubeJob) deleteJob(job batchv1.Job, config *Config) error {
	// delete pod
	opt := makePodListOptions(&job)
	podList, err := pe.client.CoreV1().Pods(config.Namespace).List(opt)
	if err != nil {
		return errors.NewNotFound(schema.GroupResource{Group: "", Resource: "Pod"}, fmt.Sprintf("get pod list fail - %v", err))
	}
	if len(podList.Items) > 0 {
		for _, pod := range podList.Items {
			err = pe.client.CoreV1().Pods(config.Namespace).Delete(pod.Name, &metav1.DeleteOptions{})
			if err != nil {
				return errors.NewInternalError(err)
			}
		}
	}

	err = pe.client.BatchV1().Jobs(config.Namespace).Delete(job.Name, &metav1.DeleteOptions{})
	if err != nil {
		return errors.NewInternalError(err)
	}
	return nil
}

func (pe *KubeJob) clearHistories(args *types.ExecuteRequest, config *Config) error {
	label := makeLabels(args.JobName, nil)
	opt := metav1.ListOptions{LabelSelector: label.AsSelector().String()}
	jobs, err := pe.client.BatchV1().Jobs(config.Namespace).List(opt)
	if err != nil {
		return errors.NewInternalError(err)
	}

	var success []batchv1.Job
	var fail []batchv1.Job
	for _, job := range jobs.Items {
		if job.Status.Succeeded > 0 {
			success = append(success, job)
		} else {
			fail = append(fail, job)
		}
	}

	if len(success) > config.SuccessfulJobsHistoryLimit {
		sort.Slice(success, func(i, j int) bool {
			return success[i].CreationTimestamp.Unix() > success[j].CreationTimestamp.Unix()
		})
		for _, oldJob := range success[config.SuccessfulJobsHistoryLimit:] {
			err = pe.deleteJob(oldJob, config)
			if err != nil {
				return errors.NewInternalError(err)
			}
		}
	}

	if len(fail) > config.FailedJobsHistoryLimit {
		sort.Slice(fail, func(i, j int) bool {
			return fail[i].CreationTimestamp.Unix() > fail[j].CreationTimestamp.Unix()
		})
		for _, oldJob := range fail[config.FailedJobsHistoryLimit:] {
			err = pe.deleteJob(oldJob, config)
			if err != nil {
				return errors.NewInternalError(err)
			}
		}
	}
	return nil
}

// 참조: https://github.com/kubernetes/client-go/blob/master/examples/in-cluster-client-configuration/main.go
func (pe *KubeJob) connect(buffer *circbuf.Buffer) error {
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		pe.logf(buffer, "get cluster config fail - %v", err)
		return err
	}
	// creates the clientset
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		pe.logf(buffer, "create clientset fail - %v", err)
		return err
	}
	pe.client = client
	return nil
}

func (pe *KubeJob) Execute(args *types.ExecuteRequest, cb plugin.StatusHelper) (*types.ExecuteResponse, error) {
	// func (pe *KubeJob) Execute(args *types.ExecuteRequest) (*types.ExecuteResponse, error) {
	output, err := circbuf.NewBuffer(maxBufSize)
	if err != nil {
		pe.logf(output, "create buffer fail - %v", err)
		return makeResponse(output, err), nil
	}
	pe.log(output, fmt.Sprintf("start pod executor - %v", args.Config))

	err = pe.connect(output)
	if err != nil {
		return makeResponse(output, err), nil
	}

	config, err := parseConfig(args)
	if err != nil {
		return makeResponse(output, err), nil
	}

	job, err := pe.client.BatchV1().Jobs(config.Namespace).Create(config.Job)
	if err != nil {
		err = errors.NewBadRequest(fmt.Sprintf("create job fail - %v", err))
		return makeResponse(output, err), nil
	}
	pe.logf(output, "create job success - %v", job.Name)

	opt := makePodListOptions(job)

	var pod v1.Pod
	phase := v1.PodUnknown
	var retry int64 = 0
	checkTerm := config.checkTerm()
	for phase != v1.PodSucceeded && phase != v1.PodFailed {
		podList, err := pe.client.CoreV1().Pods(config.Namespace).List(opt)
		if err != nil {
			err = errors.NewBadRequest(fmt.Sprintf("get pod list fail - %v", err))
			return makeResponse(output, err), nil
		}
		if len(podList.Items) == 0 {
			if config.Debug {
				pe.logf(output, "get pod list fail (option - %v)", opt)
			}
			continue
		}

		pod = podList.Items[0]
		phase = podList.Items[0].Status.Phase
		if (retry * checkTerm) < config.Timeout {
			retry = retry + 1
			time.Sleep(time.Duration(checkTerm * int64(time.Second)))
		} else {
			err = errors.NewTimeoutError(fmt.Sprintf("timeout - %v", config.Timeout), int(config.Timeout))
			return makeResponse(output, err), nil
		}
	}

	if config.Debug {
		pe.logf(output, "end pod - %v / %v", phase, pod.Name)
	}
	request := pe.client.CoreV1().Pods(config.Namespace).GetLogs(pod.Name, &v1.PodLogOptions{})
	podLogs, err := request.Stream()
	if err != nil {
		err = errors.NewBadRequest(fmt.Sprintf("get logs fail - %v", err))
		return makeResponse(output, err), nil
	}

	pe.log(output, "---------- output: Start ----------")
	_, err = io.Copy(output, podLogs)
	pe.log(output, "---------- output: End ----------")
	if err != nil {
		err = errors.NewBadRequest(fmt.Sprintf("copy log to output fail - %v", err))
		return makeResponse(output, err), nil
	}

	err = pe.clearHistories(args, config)
	if err != nil {
		pe.logf(output, "clear histories fail - %v", err)
		return makeResponse(output, err), nil
	}
	if config.Debug {
		pe.logf(output, "clear histories success.")
	}

	return makeResponse(output, err), nil
}
