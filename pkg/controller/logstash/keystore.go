// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

package logstash

import (
	logstashv1alpha1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/logstash/v1alpha1"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/keystore"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/reconciler"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/tracing"
	"hash"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	KeystoreCommand = "/usr/share/logstash/bin/logstash-keystore "
)

// newInitContainersParameters is used to generate the init container that will load the secure settings into a keystore
func newInitContainersParameters() (keystore.InitContainerParameters, error) {
	parameters := keystore.InitContainerParameters{
		KeystoreCreateCommand:         KeystoreCommand + "create",
		KeystoreAddCommand:            KeystoreCommand + `add "$key" --stdin < "$filename"`,
		SecureSettingsVolumeMountPath: keystore.SecureSettingsVolumeMountPath,
		KeystoreVolumePath:            ConfigMountPath,
		Resources: corev1.ResourceRequirements{
			Requests: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceMemory: resource.MustParse("1Gi"),
				corev1.ResourceCPU:    resource.MustParse("1000m"),
			},
			Limits: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceMemory: resource.MustParse("1Gi"),
				corev1.ResourceCPU:    resource.MustParse("1000m"),
			},
		},
	}

	return parameters, nil
}

func reconcileKeystore(params Params, configHash hash.Hash) (*keystore.Resources, *reconciler.Results) {
	defer tracing.Span(&params.Context)()
	results := reconciler.NewResult(params.Context)

	initContainersParameters, err := newInitContainersParameters()
	if err != nil {
		return nil, results.WithError(err)
	}
	// setup a keystore with secure settings in an init container, if specified by the user
	keystoreResources, err := keystore.ReconcileResources(
		params.Context,
		params,
		&params.Logstash,
		logstashv1alpha1.Namer,
		NewLabels(params.Logstash),
		initContainersParameters,
	)
	if err != nil {
		return nil, results.WithError(err)
	}

	if keystoreResources != nil {
		_, _ = configHash.Write([]byte(keystoreResources.Version))
	}

	return keystoreResources, results
}
