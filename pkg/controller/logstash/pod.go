// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

package logstash

import (
	"fmt"
	commonv1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/common/v1"
	logstashv1alpha1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/logstash/v1alpha1"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/certificates"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/container"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/defaults"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/keystore"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/tracing"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/volume"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/logstash/network"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/logstash/sset"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/logstash/stackmon"
	"github.com/elastic/cloud-on-k8s/v2/pkg/utils/maps"
	"hash"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"path"
)

const (
	CAFileName = "ca.crt"

	ContainerName = "logstash"

	ConfigVolumeName = "config"
	ConfigMountPath  = "/usr/share/logstash/config"

	LogstashConfigVolumeName = "logstash"
	LogstashConfigFileName   = "logstash.yml"

	PipelineVolumeName = "pipeline"
	PipelineFileName   = "pipelines.yml"

	ElasticsearchVolumeName = "elasticsearch-ref"
	ElasticsearchFileName   = "elasticsearch-ref.yml"

	// ConfigHashAnnotationName is an annotation used to store the Logstash config hash.
	ConfigHashAnnotationName = "logstash.k8s.elastic.co/config-hash"

	// VersionLabelName is a label used to track the version of a Logstash Pod.
	VersionLabelName = "logstash.k8s.elastic.co/version"
)

var (
	// ConfigVolume is used to propagate the keystore file from the init container to
	// Logstash main container.
	ConfigVolume = volume.NewEmptyDirVolume(ConfigVolumeName, ConfigMountPath)

	DefaultResources = corev1.ResourceRequirements{
		Limits: map[corev1.ResourceName]resource.Quantity{
			corev1.ResourceMemory: resource.MustParse("2Gi"),
			corev1.ResourceCPU:    resource.MustParse("2000m"),
		},
		Requests: map[corev1.ResourceName]resource.Quantity{
			corev1.ResourceMemory: resource.MustParse("2Gi"),
			corev1.ResourceCPU:    resource.MustParse("1000m"),
		},
	}
)

func buildPodTemplate(params Params, configHash hash.Hash32, keystoreResources *keystore.Resources) (corev1.PodTemplateSpec, error) {
	defer tracing.Span(&params.Context)()
	spec := &params.Logstash.Spec
	builder := defaults.NewPodTemplateBuilder(params.GetPodTemplate(), ContainerName)
	vols := []volume.VolumeLike{
		ConfigVolume,
		// volume with logstash configuration file
		volume.NewSecretVolume(
			logstashv1alpha1.ConfigSecretName(params.Logstash.Name),
			LogstashConfigVolumeName,
			path.Join(ConfigMountPath, LogstashConfigFileName),
			LogstashConfigFileName,
			0644),
		// volume with logstash pipeline file
		volume.NewSecretVolume(
			logstashv1alpha1.PipelineSecretName(params.Logstash.Name),
			PipelineVolumeName,
			path.Join(ConfigMountPath, PipelineFileName),
			PipelineFileName,
			0644),
		// volume with elasticsearch-ref file
		volume.NewSecretVolume(
			logstashv1alpha1.ElasticsearchRefSecretName(params.Logstash.Name),
			ElasticsearchVolumeName,
			path.Join(ConfigMountPath, ElasticsearchFileName),
			ElasticsearchFileName,
			0644),
	}

	// all volumes with CAs of direct associations
	caAssocVols, err := getVolumesFromAssociations(params.Logstash.GetAssociations())
	if err != nil {
		return corev1.PodTemplateSpec{}, err
	}
	vols = append(vols, caAssocVols...)

	labels := maps.Merge(NewLabels(params.Logstash), map[string]string{
		VersionLabelName: spec.Version})

	annotations := map[string]string{
		ConfigHashAnnotationName: fmt.Sprint(configHash.Sum32()),
	}

	ports := getDefaultContainerPorts(params.Logstash)

	builder = builder.
		WithResources(DefaultResources).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithDockerImage(spec.Image, container.ImageRepository(container.LogstashImage, spec.Version)).
		WithAutomountServiceAccountToken().
		WithPorts(ports).
		WithReadinessProbe(readinessProbe(false)).
		WithLivenessProbe(livenessProbe(false)).
		WithVolumeLikes(vols...).
		WithInitContainers(initConfigContainer())

	builder, err = stackmon.WithMonitoring(params.Context, params.Client, builder, params.Logstash)
	if err != nil {
		return corev1.PodTemplateSpec{}, err
	}

	//TODO allow keystore create without password
	if keystoreResources != nil {
		keystorePass := corev1.EnvVar{Name: "LOGSTASH_KEYSTORE_PASS", Value: "elastic"}

		keystoreResources.InitContainer.Env = append(keystoreResources.InitContainer.Env, keystorePass)

		builder.
			WithEnv(keystorePass).
			WithVolumes(keystoreResources.Volume).
			WithInitContainers(keystoreResources.InitContainer)
	}

	//TODO integrate with api.ssl.enabled
	if params.Logstash.Spec.HTTP.TLS.Enabled() {
		httpVol := certificates.HTTPCertSecretVolume(logstashv1alpha1.Namer, params.Logstash.Name)
		builder.
			WithVolumes(httpVol.Volume()).
			WithVolumeMounts(httpVol.VolumeMount())
	}

	builder.WithInitContainerDefaults()

	// add default volumeMount for PVC if not defined in spec
	if params.Logstash.Spec.StatefulSet != nil {
		builder.WithVolumeMounts(sset.DefaultDataVolumeMount)
	}

	return builder.PodTemplate, nil
}

func getVolumesFromAssociations(associations []commonv1.Association) ([]volume.VolumeLike, error) {
	var vols []volume.VolumeLike //nolint:prealloc
	for i, assoc := range associations {
		assocConf, err := assoc.AssociationConf()
		if err != nil {
			return nil, err
		}
		if !assocConf.CAIsConfigured() {
			// skip as there is no volume to mount if association has no CA configured
			continue
		}
		caSecretName := assocConf.GetCASecretName()
		vols = append(vols, volume.NewSecretVolumeWithMountPath(
			caSecretName,
			fmt.Sprintf("%s-certs-%d", assoc.AssociationType(), i),
			certificatesDir(assoc),
		))
	}
	return vols, nil
}

func certificatesDir(association commonv1.Association) string {
	ref := association.AssociationRef()
	return fmt.Sprintf(
		"/mnt/elastic-internal/%s-association/%s/%s/certs",
		association.AssociationType(),
		ref.Namespace,
		ref.NameOrSecretName(),
	)
}

func getDefaultContainerPorts(logstash logstashv1alpha1.Logstash) []corev1.ContainerPort {
	return []corev1.ContainerPort{
		{Name: logstash.Spec.HTTP.Protocol(), ContainerPort: int32(network.HTTPPort), Protocol: corev1.ProtocolTCP},
	}
}

// readinessProbe is the readiness probe for the Logstash container
func readinessProbe(useTLS bool) corev1.Probe {
	scheme := corev1.URISchemeHTTP
	if useTLS {
		scheme = corev1.URISchemeHTTPS
	}
	return corev1.Probe{
		FailureThreshold:    3,
		InitialDelaySeconds: 30,
		PeriodSeconds:       10,
		SuccessThreshold:    1,
		TimeoutSeconds:      5,
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Port:   intstr.FromInt(network.HTTPPort),
				Path:   "/",
				Scheme: scheme,
			},
		},
	}
}

// livenessProbe is the liveness probe for the Logstash container
func livenessProbe(useTLS bool) corev1.Probe {
	scheme := corev1.URISchemeHTTP
	if useTLS {
		scheme = corev1.URISchemeHTTPS
	}
	return corev1.Probe{
		FailureThreshold:    3,
		InitialDelaySeconds: 60,
		PeriodSeconds:       10,
		SuccessThreshold:    1,
		TimeoutSeconds:      5,
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Port:   intstr.FromInt(network.HTTPPort),
				Path:   "/",
				Scheme: scheme,
			},
		},
	}
}
