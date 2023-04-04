// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

//go:build logstash || e2e

package logstash

import (
	corev1 "k8s.io/api/core/v1"
	"testing"

	commonv1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/common/v1"
	"github.com/elastic/cloud-on-k8s/v2/test/e2e/test"
	"github.com/elastic/cloud-on-k8s/v2/test/e2e/test/logstash"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPipelineConfigRefLogstash(t *testing.T) {
	secretName := "ls-generator-pipeline"

	pipelineSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: test.Ctx().ManagedNamespace(0),
		},
		StringData: map[string]string{
			"pipelines.yml": `
- pipeline.id: generator
  pipeline.workers: 1
  queue.drain: false
  config.string: input { generator {} } filter { sleep { time => 10 } } output { stdout { codec => dots } }
- pipeline.id: main
  config.string: input { stdin{} } output { stdout{} }`,
		},
	}

	before := test.StepsFunc(func(k *test.K8sClient) test.StepList {
		return test.StepList{}.WithStep(test.Step{
			Name: "Create pipeline secret",
			Test: test.Eventually(func() error {
				return k.CreateOrUpdateSecrets(pipelineSecret)
			}),
		})
	})

	name := "test-pipeline-ref"
	b := logstash.NewBuilder(name).
		WithNodeCount(1).
		WithPipelinesConfigRef(commonv1.ConfigSource{
			SecretRef: commonv1.SecretRef{
				SecretName: secretName,
			},
		})

	steps := test.StepsFunc(func(k *test.K8sClient) test.StepList {
		return test.StepList{
			b.CheckMetricsRequest(k,
				logstash.Request{
					Name: "pipeline [generator]",
					Path: "/_node/pipelines/generator",
				},
				logstash.Want{
					Status: "green",
					Match:  map[string]string{"pipelines.generator.workers": "1"},
				}),
		}
	})

	test.Sequence(before, steps, b).RunSequential(t)
}

func TestPipelineConfigLogstash(t *testing.T) {
	secretName := "ls-split-pipe"

	pipelineSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: test.Ctx().ManagedNamespace(0),
		},
		StringData: map[string]string{
			"split.conf": "input { exec { command => \"uptime\" interval => 10 } } output { stdout{} }",
		},
	}

	before := test.StepsFunc(func(k *test.K8sClient) test.StepList {
		return test.StepList{}.WithStep(test.Step{
			Name: "Create pipeline secret",
			Test: test.Eventually(func() error {
				return k.CreateOrUpdateSecrets(pipelineSecret)
			}),
		})
	})

	name := "test-split-pipeline"
	volName := "ls-pipe-vol"
	mountPath := "/usr/share/logstash/pipeline"

	b := logstash.NewBuilder(name).
		WithNodeCount(1).
		WithPipelines([]commonv1.Config{
			{
				Data: map[string]interface{}{
					"pipeline.id": "split",
					"path.config": mountPath,
				},
			},
			{
				Data: map[string]interface{}{
					"pipeline.id":   "main",
					"config.string": "input { stdin{} } output { stdout{} }",
				},
			},
		}).
		WithVolumes(corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		}).
		WithVolumeMounts(corev1.VolumeMount{
			Name:      volName,
			MountPath: mountPath,
		})

	steps := test.StepsFunc(func(k *test.K8sClient) test.StepList {
		return test.StepList{
			b.CheckMetricsRequest(k,
				logstash.Request{
					Name: "pipeline [split]",
					Path: "/_node/pipelines/split",
				},
				logstash.Want{
					Status: "green",
					Match:  map[string]string{"pipelines.split.workers": "2"},
				}),
		}
	})

	test.Sequence(before, steps, b).RunSequential(t)
}
