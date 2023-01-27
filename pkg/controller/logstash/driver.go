// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

package logstash

import (
	"context"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/certificates"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/defaults"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/events"
	"hash/fnv"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	logstashv1alpha1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/logstash/v1alpha1"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/association"
	commonassociation "github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/association"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/operator"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/reconciler"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/tracing"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/version"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/watches"
	"github.com/elastic/cloud-on-k8s/v2/pkg/utils/k8s"
	"github.com/elastic/cloud-on-k8s/v2/pkg/utils/log"
)

// Params are a set of parameters used during internal reconciliation of Logstash.
type Params struct {
	Context context.Context

	Client        k8s.Client
	EventRecorder record.EventRecorder
	Watches       watches.DynamicWatches

	Logstash logstashv1alpha1.Logstash
	Status   logstashv1alpha1.LogstashStatus

	OperatorParams operator.Parameters
}

// K8sClient returns the Kubernetes client.
func (p Params) K8sClient() k8s.Client {
	return p.Client
}

// Recorder returns the Kubernetes event recorder.
func (p Params) Recorder() record.EventRecorder {
	return p.EventRecorder
}

// DynamicWatches returns the set of stateful dynamic watches used during reconciliation.
func (p Params) DynamicWatches() watches.DynamicWatches {
	return p.Watches
}

// GetPodTemplate returns the configured pod template for the associated Elastic Logstash.
func (p *Params) GetPodTemplate() corev1.PodTemplateSpec {
	return p.Logstash.Spec.Deployment.PodTemplate
}

// Logger returns the configured logger for use during reconciliation.
func (p *Params) Logger() logr.Logger {
	return log.FromContext(p.Context)
}

func newStatus(logstash logstashv1alpha1.Logstash) logstashv1alpha1.LogstashStatus {
	status := logstash.Status
	status.ObservedGeneration = logstash.Generation
	return status
}

func internalReconcile(params Params) (*reconciler.Results, logstashv1alpha1.LogstashStatus) {
	defer tracing.Span(&params.Context)()
	results := reconciler.NewResult(params.Context)

	logstashVersion, err := version.Parse(params.Logstash.Spec.Version)
	if err != nil {
		return results.WithError(err), params.Status
	}
	assocAllowed, err := association.AllowVersion(logstashVersion, &params.Logstash, params.Logger(), params.EventRecorder)
	if err != nil {
		return results.WithError(err), params.Status
	}
	if !assocAllowed {
		return results, params.Status // will eventually retry
	}

	svc, err := common.ReconcileService(params.Context, params.Client, newService(params.Logstash), &params.Logstash)
	if err != nil {
		return results.WithError(err), params.Status
	}

	_, results = certificates.Reconciler{
		K8sClient:             params.Client,
		DynamicWatches:        params.Watches,
		Owner:                 &params.Logstash,
		TLSOptions:            params.Logstash.Spec.HTTP.TLS,
		Namer:                 Namer,
		Labels:                NewLabels(params.Logstash),
		Services:              []corev1.Service{*svc},
		GlobalCA:              params.OperatorParams.GlobalCA,
		CACertRotation:        params.OperatorParams.CACertRotation,
		CertRotation:          params.OperatorParams.CertRotation,
		GarbageCollectSecrets: true,
	}.ReconcileCAAndHTTPCerts(params.Context)
	if results.HasError() {
		_, err := results.Aggregate()
		k8s.EmitErrorEvent(params.Recorder(), err, &params.Logstash, events.EventReconciliationError, "Certificate reconciliation error: %v", err)
		return results, params.Status
	}

	configHash := fnv.New32a()

	if res := reconcileConfig(params, configHash); res.HasError() {
		return results.WithResults(res), params.Status
	}

	if res := reconcileAssociationConfig(params, configHash); res.HasError() {
		return results.WithResults(res), params.Status
	}

	if res := reconcilePipeline(params, configHash); res.HasError() {
		return results.WithResults(res), params.Status
	}

	keystoreResources, res := reconcileKeystore(params, configHash)
	if res.HasError() {
		return results.WithResults(res), params.Status
	}

	// we need to deref the secret here (if any) to include it in the configHash otherwise Logstash will not be rolled on content changes
	if err := commonassociation.WriteAssocsToConfigHash(params.Client, params.Logstash.GetAssociations(), configHash); err != nil {
		return results.WithError(err), params.Status
	}

	podTemplate, err := buildPodTemplate(params, configHash, keystoreResources)
	if err != nil {
		return results.WithError(err), params.Status
	}
	return reconcilePodVehicle(params, podTemplate)
}

func newService(logstash logstashv1alpha1.Logstash) *corev1.Service {
	svc := corev1.Service{
		ObjectMeta: logstash.Spec.HTTP.Service.ObjectMeta,
		Spec:       logstash.Spec.HTTP.Service.Spec,
	}

	svc.ObjectMeta.Namespace = logstash.Namespace
	svc.ObjectMeta.Name = HTTPServiceName(logstash.Name)

	labels := NewLabels(logstash)
	ports := []corev1.ServicePort{
		{
			Name:     logstash.Spec.HTTP.Protocol(),
			Protocol: corev1.ProtocolTCP,
			Port:     HTTPPort,
		},
	}
	return defaults.SetServiceDefaults(&svc, labels, labels, ports)
}
