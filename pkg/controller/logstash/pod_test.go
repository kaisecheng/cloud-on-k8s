// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

package logstash

import (
	"context"
	"encoding/base64"
	"hash/fnv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1 "github.com/elastic/cloud-on-k8s/v3/pkg/apis/common/v1"
	logstashv1alpha1 "github.com/elastic/cloud-on-k8s/v3/pkg/apis/logstash/v1alpha1"
	"github.com/elastic/cloud-on-k8s/v3/pkg/controller/common/container"
	"github.com/elastic/cloud-on-k8s/v3/pkg/controller/common/pod"
	"github.com/elastic/cloud-on-k8s/v3/pkg/controller/common/version"
	"github.com/elastic/cloud-on-k8s/v3/pkg/controller/common/watches"
	"github.com/elastic/cloud-on-k8s/v3/pkg/controller/logstash/configs"
	lslabels "github.com/elastic/cloud-on-k8s/v3/pkg/controller/logstash/labels"
	"github.com/elastic/cloud-on-k8s/v3/pkg/utils/k8s"
)

func TestNewPodTemplateSpec(t *testing.T) {
	testHTTPCertsInternalSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-ls-http-certs-internal",
			Namespace: "default",
		},
	}

	meta := metav1.ObjectMeta{
		Name:      "fake",
		Namespace: "default",
	}

	tests := []struct {
		name            string
		logstash        logstashv1alpha1.Logstash
		apiServerConfig configs.APIServer
		assertions      func(pod corev1.PodTemplateSpec)
	}{
		{
			name: "defaults",
			logstash: logstashv1alpha1.Logstash{
				ObjectMeta: meta,
				Spec: logstashv1alpha1.LogstashSpec{
					Version: "8.6.1",
				},
			},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Equal(t, false, *pod.Spec.AutomountServiceAccountToken)
				assert.Len(t, pod.Spec.Containers, 1)
				assert.Len(t, pod.Spec.InitContainers, 1)
				assert.Len(t, pod.Spec.Volumes, 5)
				assert.NotEmpty(t, pod.Annotations[ConfigHashAnnotationName])
				logstashContainer := GetLogstashContainer(pod.Spec)
				require.NotNil(t, logstashContainer)
				assert.Equal(t, 5, len(logstashContainer.VolumeMounts))
				assert.Equal(t, container.ImageRepository(container.LogstashImage, version.MustParse("8.6.1")), logstashContainer.Image)
				assert.NotNil(t, logstashContainer.ReadinessProbe)
				assert.NotEmpty(t, logstashContainer.Ports)
			},
		},
		{
			name: "with custom image",
			logstash: logstashv1alpha1.Logstash{
				ObjectMeta: meta,
				Spec: logstashv1alpha1.LogstashSpec{
					Image:   "my-custom-image:1.0.0",
					Version: "8.6.1",
				},
			},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Equal(t, "my-custom-image:1.0.0", GetLogstashContainer(pod.Spec).Image)
			},
		},
		{
			name: "with default resources",
			logstash: logstashv1alpha1.Logstash{ObjectMeta: meta, Spec: logstashv1alpha1.LogstashSpec{
				Version: "8.6.1",
			}},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Equal(t, DefaultResources, GetLogstashContainer(pod.Spec).Resources)
			},
		},
		{
			name: "with user-provided resources",
			logstash: logstashv1alpha1.Logstash{ObjectMeta: meta, Spec: logstashv1alpha1.LogstashSpec{
				Version: "8.6.1",
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "logstash",
								Resources: corev1.ResourceRequirements{
									Limits: map[corev1.ResourceName]resource.Quantity{
										corev1.ResourceMemory: resource.MustParse("3Gi"),
									},
								},
							},
						},
					},
				},
			}},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Equal(t, corev1.ResourceRequirements{
					Limits: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceMemory: resource.MustParse("3Gi"),
					},
				}, GetLogstashContainer(pod.Spec).Resources)
			},
		},
		{
			name: "with user-provided init containers",
			logstash: logstashv1alpha1.Logstash{ObjectMeta: meta, Spec: logstashv1alpha1.LogstashSpec{
				Version: "8.6.1",
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						InitContainers: []corev1.Container{
							{
								Name: "user-init-container",
							},
						},
					},
				},
			}},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Len(t, pod.Spec.InitContainers, 2)
				assert.Equal(t, pod.Spec.Containers[0].Image, pod.Spec.InitContainers[0].Image)
			},
		},
		{
			name: "with user-provided labels",
			logstash: logstashv1alpha1.Logstash{
				ObjectMeta: meta,
				Spec: logstashv1alpha1.LogstashSpec{
					PodTemplate: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"label1":               "value1",
								"label2":               "value2",
								lslabels.NameLabelName: "overridden-logstash-name",
							},
						},
					},
					Version: "8.6.1",
				}},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				labels := (&logstashv1alpha1.Logstash{ObjectMeta: metav1.ObjectMeta{Name: "logstash-name"}}).GetIdentityLabels()
				labels[VersionLabelName] = "8.6.1"
				labels["label1"] = "value1"
				labels["label2"] = "value2"
				labels[lslabels.NameLabelName] = "overridden-logstash-name"
				labels["logstash.k8s.elastic.co/statefulset-name"] = "fake-ls"
				assert.Equal(t, labels, pod.Labels)
			},
		},
		{
			name: "with user-provided ENV variable",
			logstash: logstashv1alpha1.Logstash{ObjectMeta: meta, Spec: logstashv1alpha1.LogstashSpec{
				Version: "8.6.1",
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "logstash",
								Env: []corev1.EnvVar{
									{
										Name:  "user-env",
										Value: "user-env-value",
									},
								},
							},
						},
					},
				},
			}},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Len(t, GetLogstashContainer(pod.Spec).Env, 1)
			},
		},
		{
			name: "with multiple services, readiness probe hits the correct port",
			logstash: logstashv1alpha1.Logstash{
				ObjectMeta: meta,
				Spec: logstashv1alpha1.LogstashSpec{
					Version: "8.6.1",
					Services: []logstashv1alpha1.LogstashService{{
						Name: LogstashAPIServiceName,
						Service: commonv1.ServiceTemplate{
							Spec: corev1.ServiceSpec{
								Ports: []corev1.ServicePort{
									{Name: "api", Protocol: "TCP", Port: 9200},
								},
							},
						}}, {
						Name: "notapi",
						Service: commonv1.ServiceTemplate{
							Spec: corev1.ServiceSpec{
								Ports: []corev1.ServicePort{
									{Name: "notapi", Protocol: "TCP", Port: 9600},
								},
							},
						}},
					},
				},
			},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Equal(t, 9200, GetLogstashContainer(pod.Spec).ReadinessProbe.HTTPGet.Port.IntValue())
			},
		},
		{
			name: "with api service customized, readiness probe hits the correct port",
			logstash: logstashv1alpha1.Logstash{
				ObjectMeta: meta,
				Spec: logstashv1alpha1.LogstashSpec{
					Version: "8.6.1",
					Services: []logstashv1alpha1.LogstashService{
						{
							Name: LogstashAPIServiceName,
							Service: commonv1.ServiceTemplate{
								Spec: corev1.ServiceSpec{
									Ports: []corev1.ServicePort{
										{Name: "api", Protocol: "TCP", Port: 9200},
									},
								},
							}},
					},
				}},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Equal(t, 9200, GetLogstashContainer(pod.Spec).ReadinessProbe.HTTPGet.Port.IntValue())
			},
		},
		{
			name: "with basic auth set, readiness probe creates Authorization header",
			logstash: logstashv1alpha1.Logstash{
				ObjectMeta: meta,
				Spec: logstashv1alpha1.LogstashSpec{
					Version: "8.6.1",
				}},
			apiServerConfig: GetAPIServerWithAuth(),
			assertions: func(pod corev1.PodTemplateSpec) {
				authHeader := GetLogstashContainer(pod.Spec).ReadinessProbe.HTTPGet.HTTPHeaders[0]
				b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(authHeader.Value, "Basic "))
				assert.Equal(t, "Authorization", authHeader.Name)
				assert.Equal(t, "logstash:whatever", string(b))
			},
		},
		{
			name: "with tls set, readiness probe use https protocol",
			logstash: logstashv1alpha1.Logstash{
				ObjectMeta: meta,
				Spec: logstashv1alpha1.LogstashSpec{
					Version: "8.6.1",
				}},
			apiServerConfig: GetAPIServerWithAuth(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.NotNil(t, GetEnvByName(GetConfigInitContainer(pod.Spec).Env, UseTLSEnv))
				assert.NotNil(t, GetEnvByName(GetConfigInitContainer(pod.Spec).Env, APIKeystorePassEnv))
				assert.Equal(t, corev1.URISchemeHTTPS, GetLogstashContainer(pod.Spec).ReadinessProbe.HTTPGet.Scheme)
			},
		},
		{
			name: "with default service, readiness probe hits the correct port",
			logstash: logstashv1alpha1.Logstash{
				ObjectMeta: meta,
				Spec: logstashv1alpha1.LogstashSpec{
					Version: "8.6.1",
				}},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Equal(t, 9600, GetLogstashContainer(pod.Spec).ReadinessProbe.HTTPGet.Port.IntValue())
			},
		},

		{
			name: "with custom annotation",
			logstash: logstashv1alpha1.Logstash{ObjectMeta: meta, Spec: logstashv1alpha1.LogstashSpec{
				Image:   "my-custom-image:1.0.0",
				Version: "8.6.1",
			}},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Equal(t, "my-custom-image:1.0.0", GetLogstashContainer(pod.Spec).Image)
			},
		},
		{
			name: "with user-provided volumes and volume mounts",
			logstash: logstashv1alpha1.Logstash{ObjectMeta: meta, Spec: logstashv1alpha1.LogstashSpec{
				Version: "8.6.1",
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "logstash",
								VolumeMounts: []corev1.VolumeMount{
									{
										Name: "user-volume-mount",
									},
								},
							},
						},
						Volumes: []corev1.Volume{
							{
								Name: "user-volume",
							},
						},
					},
				},
			}},
			apiServerConfig: GetDefaultAPIServer(),
			assertions: func(pod corev1.PodTemplateSpec) {
				assert.Len(t, pod.Spec.Volumes, 6)
				assert.Len(t, GetLogstashContainer(pod.Spec).VolumeMounts, 6)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := Params{
				Context:         context.Background(),
				Client:          k8s.NewFakeClient(&testHTTPCertsInternalSecret),
				Logstash:        tt.logstash,
				APIServerConfig: tt.apiServerConfig,
				Watches:         watches.NewDynamicWatches(),
			}
			configHash := fnv.New32a()
			got, err := buildPodTemplate(params, configHash)

			require.NoError(t, err)
			tt.assertions(got)
		})
	}
}

func TestWriteUserCertSecretsToConfigHash(t *testing.T) {
	ls := logstashv1alpha1.Logstash{
		ObjectMeta: metav1.ObjectMeta{Name: "logstash-sample", Namespace: "default"},
	}

	certSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-certs", Namespace: "default"},
		Data: map[string][]byte{
			"tls.crt":    []byte("cert-data"),
			"tls.key":    []byte("key-data"),
			"config.yml": []byte("non-cert-data"),
		},
	}

	withSecretVolume := func(secretName string) logstashv1alpha1.Logstash {
		ls := ls
		ls.Spec.PodTemplate.Spec.Volumes = []corev1.Volume{
			{Name: "certs", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secretName}}},
		}
		return ls
	}

	hashOf := func(params Params) uint32 {
		h := fnv.New32a()
		err := writeUserCertSecretsToConfigHash(params, h)
		require.NoError(t, err)
		return h.Sum32()
	}

	t.Run("cert and key values contribute to hash", func(t *testing.T) {
		paramsWithCerts := Params{
			Context:  context.Background(),
			Client:   k8s.NewFakeClient(&certSecret),
			Logstash: withSecretVolume("my-certs"),
			Watches:  watches.NewDynamicWatches(),
		}
		emptyHash := fnv.New32a().Sum32()
		assert.NotEqual(t, emptyHash, hashOf(paramsWithCerts))
	})

	t.Run("hash changes when cert value changes", func(t *testing.T) {
		params := func(certData string) Params {
			s := certSecret.DeepCopy()
			s.Data["tls.crt"] = []byte(certData)
			return Params{
				Context:  context.Background(),
				Client:   k8s.NewFakeClient(s),
				Logstash: withSecretVolume("my-certs"),
				Watches:  watches.NewDynamicWatches(),
			}
		}
		assert.NotEqual(t, hashOf(params("old-cert")), hashOf(params("new-cert")))
	})

	t.Run("non-cert keys do not affect hash", func(t *testing.T) {
		params := func(configData string) Params {
			s := certSecret.DeepCopy()
			s.Data["config.yml"] = []byte(configData)
			return Params{
				Context:  context.Background(),
				Client:   k8s.NewFakeClient(s),
				Logstash: withSecretVolume("my-certs"),
				Watches:  watches.NewDynamicWatches(),
			}
		}
		assert.Equal(t, hashOf(params("value-a")), hashOf(params("value-b")))
	})

	t.Run("missing secret is skipped without error", func(t *testing.T) {
		params := Params{
			Context:  context.Background(),
			Client:   k8s.NewFakeClient(),
			Logstash: withSecretVolume("does-not-exist"),
			Watches:  watches.NewDynamicWatches(),
		}
		h := fnv.New32a()
		require.NoError(t, writeUserCertSecretsToConfigHash(params, h))
		assert.Equal(t, fnv.New32a().Sum32(), h.Sum32())
	})

	t.Run("no volumes produces no hash contribution", func(t *testing.T) {
		params := Params{
			Context:  context.Background(),
			Client:   k8s.NewFakeClient(&certSecret),
			Logstash: ls,
			Watches:  watches.NewDynamicWatches(),
		}
		h := fnv.New32a()
		require.NoError(t, writeUserCertSecretsToConfigHash(params, h))
		assert.Equal(t, fnv.New32a().Sum32(), h.Sum32())
	})

	t.Run("multiple secret volumes all contribute to hash", func(t *testing.T) {
		secret1 := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "certs-a"},
			Data:       map[string][]byte{"tls.crt": []byte("cert-a-data")},
		}
		secret2 := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "certs-b"},
			Data:       map[string][]byte{"tls.crt": []byte("cert-b-data")},
		}
		withTwoVolumes := logstashv1alpha1.Logstash{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "ls"},
			Spec: logstashv1alpha1.LogstashSpec{
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{
							{Name: "vol-a", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "certs-a"}}},
							{Name: "vol-b", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "certs-b"}}},
						},
					},
				},
			},
		}

		hashBoth := fnv.New32a()
		require.NoError(t, writeUserCertSecretsToConfigHash(Params{
			Context:  context.Background(),
			Client:   k8s.NewFakeClient(&secret1, &secret2),
			Logstash: withTwoVolumes,
			Watches:  watches.NewDynamicWatches(),
		}, hashBoth))
		sumBoth := hashBoth.Sum32()

		hashOnlyA := fnv.New32a()
		require.NoError(t, writeUserCertSecretsToConfigHash(Params{
			Context:  context.Background(),
			Client:   k8s.NewFakeClient(&secret1),
			Logstash: withTwoVolumes,
			Watches:  watches.NewDynamicWatches(),
		}, hashOnlyA))
		sumOnlyA := hashOnlyA.Sum32()

		assert.NotEqual(t, sumBoth, sumOnlyA, "removing a secret should change the hash")
	})

	t.Run("hash is deterministic across repeated calls", func(t *testing.T) {
		params := Params{
			Context:  context.Background(),
			Client:   k8s.NewFakeClient(&certSecret),
			Logstash: withSecretVolume("my-certs"),
			Watches:  watches.NewDynamicWatches(),
		}
		assert.Equal(t, hashOf(params), hashOf(params))
	})

	t.Run("projected volume secret contributes to hash", func(t *testing.T) {
		projected := logstashv1alpha1.Logstash{
			ObjectMeta: metav1.ObjectMeta{Name: "logstash-sample", Namespace: "default"},
			Spec: logstashv1alpha1.LogstashSpec{
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{{
							Name: "projected-certs",
							VolumeSource: corev1.VolumeSource{
								Projected: &corev1.ProjectedVolumeSource{
									Sources: []corev1.VolumeProjection{{
										Secret: &corev1.SecretProjection{
											LocalObjectReference: corev1.LocalObjectReference{Name: "my-certs"},
										},
									}},
								},
							},
						}},
					},
				},
			},
		}
		params := func(certData string) Params {
			s := certSecret.DeepCopy()
			s.Data["tls.crt"] = []byte(certData)
			return Params{
				Context:  context.Background(),
				Client:   k8s.NewFakeClient(s),
				Logstash: projected,
				Watches:  watches.NewDynamicWatches(),
			}
		}
		assert.NotEqual(t, hashOf(params("old-cert")), hashOf(params("new-cert")))
	})

	t.Run("projected volume with empty secret name is skipped", func(t *testing.T) {
		projected := logstashv1alpha1.Logstash{
			ObjectMeta: metav1.ObjectMeta{Name: "logstash-sample", Namespace: "default"},
			Spec: logstashv1alpha1.LogstashSpec{
				PodTemplate: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Volumes: []corev1.Volume{{
							Name: "projected-empty",
							VolumeSource: corev1.VolumeSource{
								Projected: &corev1.ProjectedVolumeSource{
									Sources: []corev1.VolumeProjection{{
										Secret: &corev1.SecretProjection{},
									}},
								},
							},
						}},
					},
				},
			},
		}
		h := fnv.New32a()
		require.NoError(t, writeUserCertSecretsToConfigHash(Params{
			Context:  context.Background(),
			Client:   k8s.NewFakeClient(),
			Logstash: projected,
			Watches:  watches.NewDynamicWatches(),
		}, h))
		assert.Equal(t, fnv.New32a().Sum32(), h.Sum32())
	})
}

// GetLogstashContainer returns the Logstash container from the given podSpec.
func GetLogstashContainer(podSpec corev1.PodSpec) *corev1.Container {
	return pod.ContainerByName(podSpec, logstashv1alpha1.LogstashContainerName)
}

func GetConfigInitContainer(podSpec corev1.PodSpec) *corev1.Container {
	return pod.InitContainerByName(podSpec, InitConfigContainerName)
}

func GetEnvByName(envs []corev1.EnvVar, name string) *corev1.EnvVar {
	for i, e := range envs {
		if e.Name == name {
			return &envs[i]
		}
	}
	return nil
}

func GetAPIServerWithAuth() configs.APIServer {
	return configs.APIServer{
		SSLEnabled:       "true",
		KeystorePassword: "blablabla",
		AuthType:         "basic",
		Username:         "logstash",
		Password:         "whatever",
	}
}

func GetDefaultAPIServer() configs.APIServer {
	return configs.APIServer{
		SSLEnabled:       "",
		KeystorePassword: APIKeystoreDefaultPass,
		AuthType:         "",
		Username:         "",
		Password:         "",
	}
}
