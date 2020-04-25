# dkron-executor-kubejob

# [dkron](https://dkron.io/)의 executor plugin 입니다.

# 설정 - dkron job configuration 
```json
{
  "name": "test-job1",
  "schedule": "@every 1m",
  "timezone": "Asia/Seoul",
  "owner": "ruinnel",
  "owner_email": "ruinnel@gmail.com",
  "disabled": false,
  "tags": {
    "job-purpose": "test"
  },
  "concurrency": "allow",
  "executor": "kubejob",
  "executor_config": {
    "namespace": "default",
    "job": "{\"apiVersion\":\"batch/v1\",\"kind\":\"Job\",\"metadata\":{\"name\":\"dkron-test\",\"namespace\":\"default\"},\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"dkron-test-print-env\",\"image\":\"busybox:1.31.1\",\"args\":[\"env\"]}],\"restartPolicy\":\"Never\"}},\"backoffLimit\":4}}",
    "successfulJobsHistoryLimit": 3,
    "failedJobsHistoryLimit": 1,
    "timeout": 10
  }
}
```
  - `executor`: job을 실행 할 Plugin의 이름 (`kubejob`)
  - `"executor_config"` 설정
    - `namespace`: Kubernetes Job을 실행 할 namespace
    - `job`: 실행 할 Kubernetes의 [`Job`](https://kubernetes.io/ko/docs/concepts/workloads/controllers/jobs-run-to-completion/) 설정
    - `successfulJobsHistoryLimit`: [Kubernetes CronJob 설정](https://kubernetes.io/docs/tasks/job/#jobs-history-limits)과 동일. 실행 완료된 성공한 Pod을 몇개 남길지에 대한 설정.
    - `failedJobsHistoryLimit`: [Kubernetes CronJob 설정](https://kubernetes.io/docs/tasks/job/#jobs-history-limits)과 동일. 실행 완료된 실패한 Pod을 몇개 남길지에 대한 설정.
    - `timeout`: Job 실행 timeout 설정(단위: Second)
  
# 제한사항
  - [in-cluster-client-configuration](https://github.com/kubernetes/client-go/blob/master/examples/in-cluster-client-configuration/main.go) 방식으로 인증을 처리하므로 `kubernetes 클러스터 내부에 설치되고 설치된 클러스터를 대상으로 동작을 전제로 합니다.`  
  
# Dockerfile
  - `dkron/dkron:v2.2.1` 이미지에 `dkron-executor-kubejob`을 추가한 이미지를 생성 합니다.