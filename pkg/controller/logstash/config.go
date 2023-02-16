// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

package logstash

import (
	"context"
	"fmt"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/certificates"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/driver"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/events"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/labels"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/watches"
	ulog "github.com/elastic/cloud-on-k8s/v2/pkg/utils/log"
	"github.com/pkg/errors"
	"hash"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"path/filepath"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	commonv1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/common/v1"
	logstashv1alpha1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/logstash/v1alpha1"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/association"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/reconciler"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/settings"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/tracing"
	"github.com/elastic/cloud-on-k8s/v2/pkg/utils/k8s"
)

func reconcileConfig(params Params, configHash hash.Hash) *reconciler.Results {
	defer tracing.Span(&params.Context)()
	results := reconciler.NewResult(params.Context)

	cfgBytes, err := buildConfig(params)
	if err != nil {
		return results.WithError(err)
	}

	expected := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: params.Logstash.Namespace,
			Name:      logstashv1alpha1.ConfigSecretName(params.Logstash.Name),
			Labels:    labels.AddCredentialsLabel(NewLabels(params.Logstash)),
		},
		Data: map[string][]byte{
			LogstashConfigFileName: cfgBytes,
		},
	}

	if _, err = reconciler.ReconcileSecret(params.Context, params.Client, expected, &params.Logstash); err != nil {
		return results.WithError(err)
	}

	_, _ = configHash.Write(cfgBytes)

	return results
}

func buildConfig(params Params) ([]byte, error) {
	existingCfg, err := getExistingConfig(params.Context, params.Client, params.Logstash)
	if err != nil {
		return nil, err
	}

	userProvidedCfg, err := getUserConfig(params)
	if err != nil {
		return nil, err
	}

	cfg, err := defaultConfig()
	if err != nil {
		return nil, err
	}

	// merge with user settings last so they take precedence
	if err = cfg.MergeWith(existingCfg, userProvidedCfg); err != nil {
		return nil, err
	}

	return cfg.Render()
}

// getExistingConfig retrieves the canonical config, if one exists
func getExistingConfig(ctx context.Context, client k8s.Client, logstash logstashv1alpha1.Logstash) (*settings.CanonicalConfig, error) {
	var secret corev1.Secret
	key := types.NamespacedName{
		Namespace: logstash.Namespace,
		Name:      logstashv1alpha1.ConfigSecretName(logstash.Name),
	}
	err := client.Get(context.Background(), key, &secret)
	if err != nil && apierrors.IsNotFound(err) {
		ulog.FromContext(ctx).V(1).Info("Logstash config secret does not exist", "namespace", logstash.Namespace, "name", logstash.Name)
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	rawCfg, exists := secret.Data[LogstashConfigFileName]
	if !exists {
		return nil, nil
	}

	cfg, err := settings.ParseConfig(rawCfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// getUserConfig extracts the config either from the spec `config` field or from the Secret referenced by spec
// `configRef` field.
func getUserConfig(params Params) (*settings.CanonicalConfig, error) {
	if params.Logstash.Spec.Config != nil {
		return settings.NewCanonicalConfigFrom(params.Logstash.Spec.Config.Data)
	}
	return common.ParseConfigRef(params, &params.Logstash, params.Logstash.Spec.ConfigRef, LogstashConfigFileName)
}

// TODO: remove testing value
func defaultConfig() (*settings.CanonicalConfig, error) {
	settingsMap := map[string]interface{}{
		"node.name": "test",
	}

	return settings.MustCanonicalConfig(settingsMap), nil
}

func reconcileAssociationConfig(params Params, configHash hash.Hash) *reconciler.Results {
	defer tracing.Span(&params.Context)()
	results := reconciler.NewResult(params.Context)

	cfgBytes, err := buildAssociationConfig(params)
	if err != nil {
		return results.WithError(err)
	}

	expected := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: params.Logstash.Namespace,
			Name:      logstashv1alpha1.ElasticsearchRefSecretName(params.Logstash.Name),
			Labels:    labels.AddCredentialsLabel(NewLabels(params.Logstash)),
		},
		Data: map[string][]byte{
			ElasticsearchFileName: cfgBytes,
		},
	}

	if _, err = reconciler.ReconcileSecret(params.Context, params.Client, expected, &params.Logstash); err != nil {
		return results.WithError(err)
	}

	_, _ = configHash.Write(cfgBytes)

	return results
}

func buildAssociationConfig(params Params) ([]byte, error) {
	cfg, err := getAssociationConfig(params)
	if err != nil {
		return nil, err
	}

	return cfg.Render()
}

func getAssociationConfig(params Params) (*settings.CanonicalConfig, error) {
	allAssociations := params.Logstash.GetAssociations()

	var esAssociations []commonv1.Association
	for _, assoc := range allAssociations {
		if assoc.AssociationType() == commonv1.ElasticsearchAssociationType {
			esAssociations = append(esAssociations, assoc)
		}
	}

	cfg := settings.NewCanonicalConfig()
	for _, assoc := range esAssociations {
		assocConf, err := assoc.AssociationConf()
		if err != nil {
			return settings.NewCanonicalConfig(), err
		}
		if !assocConf.IsConfigured() {
			return settings.NewCanonicalConfig(), nil
		}

		credentials, err := association.ElasticsearchAuthSettings(params.Context, params.Client, assoc)
		if err != nil {
			return settings.NewCanonicalConfig(), err
		}

		esRefName := getEsRefNamespacedName(params.Logstash)
		if err := cfg.MergeWith(settings.MustCanonicalConfig(map[string]interface{}{
			esRefName + ".hosts":    assocConf.GetURL(),
			esRefName + ".username": credentials.Username,
			esRefName + ".password": credentials.Password,
		})); err != nil {
			return nil, err
		}

		if assocConf.GetCACertProvided() {
			if err := cfg.MergeWith(settings.MustCanonicalConfig(map[string]interface{}{
				esRefName + ".ssl.certificate_authority": filepath.Join(certificatesDir(assoc), certificates.CAFileName),
			})); err != nil {
				return nil, err
			}
		}

	}

	return cfg, nil
}

func getEsRefNamespacedName(logstash logstashv1alpha1.Logstash) string {
	var namespace string
	if logstash.ElasticsearchRef().Namespace == "" {
		namespace = "default"
	} else {
		namespace = logstash.ElasticsearchRef().Namespace
	}

	return fmt.Sprintf("%s.%s", namespace, logstash.ElasticsearchRef().Name)
}

func ReconcileConfigMap(
	ctx context.Context,
	c k8s.Client,
	expected corev1.ConfigMap,
	logstash *logstashv1alpha1.Logstash,
) error {
	reconciled := &corev1.ConfigMap{}
	return reconciler.ReconcileResource(
		reconciler.Params{
			Context:    ctx,
			Client:     c,
			Owner:      logstash,
			Expected:   &expected,
			Reconciled: reconciled,
			NeedsUpdate: func() bool {
				return !reflect.DeepEqual(expected.Data, reconciled.Data)
			},
			UpdateReconciled: func() {
				reconciled.Data = expected.Data
			},
		},
	)
}

func reconcilePipeline(params Params, configHash hash.Hash) *reconciler.Results {
	defer tracing.Span(&params.Context)()
	results := reconciler.NewResult(params.Context)

	cfgBytes, err := buildPipeline(params)
	if err != nil {
		return results.WithError(err)
	}

	expected := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: params.Logstash.Namespace,
			Name:      logstashv1alpha1.PipelineSecretName(params.Logstash.Name),
			Labels:    labels.AddCredentialsLabel(NewLabels(params.Logstash)),
		},
		Data: map[string][]byte{
			PipelineFileName: cfgBytes,
		},
	}

	if _, err = reconciler.ReconcileSecret(params.Context, params.Client, expected, &params.Logstash); err != nil {
		return results.WithError(err)
	}

	_, _ = configHash.Write(cfgBytes)

	return results
}

func buildPipeline(params Params) ([]byte, error) {
	userProvidedCfg, err := getUserPipeline(params)
	if err != nil {
		return nil, err
	} else if userProvidedCfg != nil {
		return userProvidedCfg.Render()
	}

	existingCfg, err := getExistingPipeline(params.Context, params.Client, params.Logstash)
	if err != nil {
		return nil, err
	} else if existingCfg != nil {
		return existingCfg.Render()
	}

	cfg, err := defaultPipeline()
	if err != nil {
		return nil, err
	} else {
		return cfg.Render()
	}
}

// getExistingPipeline retrieves the canonical pipeline, if one exists
func getExistingPipeline(ctx context.Context, client k8s.Client, logstash logstashv1alpha1.Logstash) (*Pipelines, error) {
	var secret corev1.Secret
	key := types.NamespacedName{
		Namespace: logstash.Namespace,
		Name:      logstashv1alpha1.PipelineSecretName(logstash.Name),
	}
	err := client.Get(context.Background(), key, &secret)
	if err != nil && apierrors.IsNotFound(err) {
		ulog.FromContext(ctx).V(1).Info("Logstash pipeline secret does not exist", "namespace", logstash.Namespace, "name", logstash.Name)
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	rawCfg, exists := secret.Data[PipelineFileName]
	if !exists {
		return nil, nil
	}

	cfg, err := ParsePipeline(rawCfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// getUserPipeline extracts the pipeline either from the spec `pipeline` field or from the Secret referenced by spec
// `pipelineRef` field.
func getUserPipeline(params Params) (*Pipelines, error) {
	if params.Logstash.Spec.Pipelines != nil {
		//return NewPipelineFrom(params.Logstash.Spec.Pipelines)
		return NewPipelineFrom(params.Logstash.Spec.Pipelines)
	}
	return ParseConfigRef(params, &params.Logstash, params.Logstash.Spec.PipelineRef, PipelineFileName)
}

// TODO: remove testing value
func defaultPipeline() (*Pipelines, error) {
	pipelineMap := []interface{}{
		map[string]string{
			"pipeline.id":   "demo",
			"config.string": "input { exec { command => \"uptime\" interval => 5 } } output { stdout{} }",
		},
	}

	return MustPipeline(pipelineMap), nil
}

// ConfigRefWatchName returns the name of the watch registered on the secret referenced in `configRef`.
func ConfigRefWatchName(resource types.NamespacedName) string {
	return fmt.Sprintf("%s-%s-configref", resource.Namespace, resource.Name)
}

// ParseConfigRef retrieves the content of a secret referenced in `configRef`, sets up dynamic watches for that secret,
// and parses the secret content into a Pipelines.
func ParseConfigRef(
	driver driver.Interface,
	resource runtime.Object, // eg. Beat, EnterpriseSearch
	configRef *commonv1.ConfigSource,
	secretKey string, // retrieve config data from that entry in the secret
) (*Pipelines, error) {
	resourceMeta, err := meta.Accessor(resource)
	if err != nil {
		return nil, err
	}
	namespace := resourceMeta.GetNamespace()
	resourceNsn := types.NamespacedName{Namespace: namespace, Name: resourceMeta.GetName()}

	// ensure watches match the referenced secret
	var secretNames []string
	if configRef != nil && configRef.SecretName != "" {
		secretNames = append(secretNames, configRef.SecretName)
	}
	if err := watches.WatchUserProvidedSecrets(resourceNsn, driver.DynamicWatches(), ConfigRefWatchName(resourceNsn), secretNames); err != nil {
		return nil, err
	}

	if len(secretNames) == 0 {
		// no secret referenced, nothing to do
		return nil, nil
	}

	var secret corev1.Secret
	if err := driver.K8sClient().Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: configRef.SecretName}, &secret); err != nil {
		// the secret may not exist (yet) in the cache, let's explicitly error out and retry later
		return nil, err
	}
	data, exists := secret.Data[secretKey]
	if !exists {
		msg := fmt.Sprintf("unable to parse configRef secret %s/%s: missing key %s", namespace, configRef.SecretName, secretKey)
		driver.Recorder().Event(resource, corev1.EventTypeWarning, events.EventReasonUnexpected, msg)
		return nil, errors.New(msg)
	}
	parsed, err := ParsePipeline(data)
	if err != nil {
		msg := fmt.Sprintf("unable to parse %s in configRef secret %s/%s", secretKey, namespace, configRef.SecretName)
		driver.Recorder().Event(resource, corev1.EventTypeWarning, events.EventReasonUnexpected, msg)
		return nil, errors.Wrap(err, msg)
	}
	return parsed, nil
}
