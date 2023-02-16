// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

package logstash

import (
	"context"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/logstash/sset"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/pkg/errors"

	logstashv1alpha1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/logstash/v1alpha1"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/deployment"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/reconciler"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/tracing"
	"github.com/elastic/cloud-on-k8s/v2/pkg/utils/k8s"
	"github.com/elastic/cloud-on-k8s/v2/pkg/utils/pointer"
)

func reconcilePodVehicle(params Params, podTemplate corev1.PodTemplateSpec) (*reconciler.Results, logstashv1alpha1.LogstashStatus) {
	defer tracing.Span(&params.Context)()
	results := reconciler.NewResult(params.Context)

	spec := params.Logstash.Spec

	var reconciliationFunc func(params ReconciliationParams) (int32, int32, error)
	switch {
	case spec.Deployment != nil:
		reconciliationFunc = reconcileDeployment
	case spec.StatefulSet != nil:
		reconciliationFunc = reconcileStatefulSet
	}

	ready, desired, err := reconciliationFunc(ReconciliationParams{
		ctx:         params.Context,
		client:      params.Client,
		logstash:    params.Logstash,
		podTemplate: podTemplate,
	})

	if err != nil {
		return results.WithError(err), params.Status
	}

	var status logstashv1alpha1.LogstashStatus
	if status, err = calculateStatus(&params, ready, desired); err != nil {
		err = errors.Wrap(err, "while calculating status")
	}

	return results.WithError(err), status
}

func reconcileDeployment(rp ReconciliationParams) (int32, int32, error) {
	d := deployment.New(deployment.Params{
		Name:                 logstashv1alpha1.Name(rp.logstash.Name),
		Namespace:            rp.logstash.Namespace,
		Selector:             NewLabels(rp.logstash),
		Labels:               NewLabels(rp.logstash),
		PodTemplateSpec:      rp.podTemplate,
		Replicas:             pointer.Int32OrDefault(rp.logstash.Spec.Deployment.Replicas, int32(1)),
		RevisionHistoryLimit: rp.logstash.Spec.RevisionHistoryLimit,
		Strategy:             rp.logstash.Spec.Deployment.Strategy,
	})
	if err := controllerutil.SetControllerReference(&rp.logstash, &d, scheme.Scheme); err != nil {
		return 0, 0, err
	}

	reconciled, err := deployment.Reconcile(rp.ctx, rp.client, d, &rp.logstash)
	if err != nil {
		return 0, 0, err
	}

	return reconciled.Status.ReadyReplicas, reconciled.Status.Replicas, nil
}

func reconcileStatefulSet(rp ReconciliationParams) (int32, int32, error) {
	s, _ := sset.New(sset.Params{
		Name:                 logstashv1alpha1.Name(rp.logstash.Name),
		Namespace:            rp.logstash.Namespace,
		ServiceName:          logstashv1alpha1.HTTPServiceName(rp.logstash.Name),
		Selector:             NewLabels(rp.logstash),
		Labels:               NewLabels(rp.logstash),
		PodTemplateSpec:      rp.podTemplate,
		VolumeClaimTemplates: rp.logstash.Spec.StatefulSet.VolumeClaimTemplates,
		Replicas:             pointer.Int32OrDefault(rp.logstash.Spec.StatefulSet.Replicas, int32(1)),
		RevisionHistoryLimit: rp.logstash.Spec.RevisionHistoryLimit,
	})
	if err := controllerutil.SetControllerReference(&rp.logstash, &s, scheme.Scheme); err != nil {
		return 0, 0, err
	}

	reconciled, err := sset.Reconcile(rp.ctx, rp.client, s, &rp.logstash)
	if err != nil {
		return 0, 0, err
	}

	return reconciled.Status.ReadyReplicas, reconciled.Status.Replicas, nil
}

// ReconciliationParams are the parameters used during an Elastic Logstash's reconciliation.
type ReconciliationParams struct {
	ctx         context.Context
	client      k8s.Client
	logstash    logstashv1alpha1.Logstash
	podTemplate corev1.PodTemplateSpec
}

// calculateStatus will calculate a new status from the state of the pods within the k8s cluster
// and will return any error encountered.
func calculateStatus(params *Params, ready, desired int32) (logstashv1alpha1.LogstashStatus, error) {
	logstash := params.Logstash
	status := params.Status

	pods, err := k8s.PodsMatchingLabels(params.Client, logstash.Namespace, map[string]string{NameLabelName: logstash.Name})
	if err != nil {
		return status, err
	}

	status.Version = common.LowestVersionFromPods(params.Context, status.Version, pods, VersionLabelName)
	status.AvailableNodes = ready
	status.ExpectedNodes = desired
	return status, nil
}

// updateStatus will update the Elastic Logstash's status within the k8s cluster, using the given Elastic Logstash and status.
func updateStatus(ctx context.Context, logstash logstashv1alpha1.Logstash, client client.Client, status logstashv1alpha1.LogstashStatus) error {
	if reflect.DeepEqual(logstash.Status, status) {
		return nil
	}
	logstash.Status = status
	return common.UpdateStatus(ctx, client, &logstash)
}
