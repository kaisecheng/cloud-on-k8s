// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

package logstash

import (
	"fmt"
	"hash"
	"regexp"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	logstashv1alpha1 "github.com/elastic/cloud-on-k8s/v2/pkg/apis/logstash/v1alpha1"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/labels"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/reconciler"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/settings"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/common/tracing"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/logstash/configs"
	"github.com/elastic/cloud-on-k8s/v2/pkg/controller/logstash/volume"
)

const (
	ConfigFileName         = "logstash.yml"
	APIKeystorePath        = volume.ConfigMountPath + "/" + APIKeystoreFileName
	APIKeystoreFileName    = "api_keystore.p12"  // #nosec G101
	APIKeystoreDefaultPass = "ch@ng3m3"          // #nosec G101
	APIKeystorePassEnv     = "API_KEYSTORE_PASS" // #nosec G101
)

func reconcileConfig(params Params, configHash hash.Hash) (*settings.CanonicalConfig, *configs.APIServer, error) {
	defer tracing.Span(&params.Context)()

	cfg, err := buildConfig(params)
	if err != nil {
		return nil, nil, err
	}

	apiServerConfig, err := resolveAPIServerConfig(cfg, params)
	if err != nil {
		return nil, nil, err
	}

	if err = checkTLSConfig(apiServerConfig, params.UseTLS); err != nil {
		return nil, nil, err
	}

	cfgBytes, err := cfg.Render()
	if err != nil {
		return nil, nil, err
	}

	expected := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: params.Logstash.Namespace,
			Name:      logstashv1alpha1.ConfigSecretName(params.Logstash.Name),
			Labels:    labels.AddCredentialsLabel(params.Logstash.GetIdentityLabels()),
		},
		Data: map[string][]byte{
			ConfigFileName: cfgBytes,
		},
	}

	// store the keystore password for initConfigContainer to reference,
	// so that the password does not expose in plain text
	if params.UseTLS {
		expected.Data[APIKeystorePassEnv] = []byte(apiServerConfig.KeystorePassword)
	}

	if _, err = reconciler.ReconcileSecret(params.Context, params.Client, expected, &params.Logstash); err != nil {
		return nil, nil, err
	}

	_, _ = configHash.Write(cfgBytes)

	return cfg, apiServerConfig, nil
}

func buildConfig(params Params) (*settings.CanonicalConfig, error) {
	userProvidedCfg, err := getUserConfig(params)
	if err != nil {
		return nil, err
	}

	cfg := defaultConfig()
	tls := tlsConfig(params.UseTLS)

	// merge with user settings last so they take precedence
	if err := cfg.MergeWith(tls, userProvidedCfg); err != nil {
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
	return common.ParseConfigRef(params, &params.Logstash, params.Logstash.Spec.ConfigRef, ConfigFileName)
}

func defaultConfig() *settings.CanonicalConfig {
	settingsMap := map[string]interface{}{
		// Set 'api.http.host' by default to `0.0.0.0` for readiness probe to work.
		"api.http.host": "0.0.0.0",
		// Set `config.reload.automatic` to `true` to enable pipeline reloads by default
		"config.reload.automatic": true,
	}

	return settings.MustCanonicalConfig(settingsMap)
}

func tlsConfig(useTLS bool) *settings.CanonicalConfig {
	if !useTLS {
		return nil
	}
	return settings.MustCanonicalConfig(map[string]interface{}{
		"api.ssl.enabled":           true,
		"api.ssl.keystore.path":     APIKeystorePath,
		"api.ssl.keystore.password": APIKeystoreDefaultPass,
	})
}

// checkTLSConfig ensures logstash config `api.ssl.enabled` matches the TLS setting of API service
// we allow disabling TLS in service and leaving `api.ssl.enabled` unset in logstash.yml, otherwise throw error
func checkTLSConfig(config *configs.APIServer, useTLS bool) error {
	svcUseTLS := strconv.FormatBool(useTLS)
	sslEnabled := config.SSLEnabled
	if (svcUseTLS == sslEnabled) || (!useTLS && sslEnabled == "") {
		return nil
	}

	return fmt.Errorf("API Service `spec.services.tls.selfSignedCertificate.disabled` is set to `%t`, but logstash config `api.ssl.enabled` is set to `%s`", !useTLS, sslEnabled)
}

// resolveAPIServerConfig gives ExpectedAPIServer with the resolved ${VAR} value
func resolveAPIServerConfig(cfg *settings.CanonicalConfig, params Params) (*configs.APIServer, error) {
	config := baseAPIServer(cfg)

	if unresolvedConfig := patchWithPatternValue(config); len(unresolvedConfig) > 0 {
		combinedMap, err := getKeystoreEnvKeyValue(params)
		if err != nil {
			return nil, err
		}

		resolveConfigValue(unresolvedConfig, combinedMap)
	}

	return config, nil
}

// baseAPIServer gives api.* configs with the default value
func baseAPIServer(cfg *settings.CanonicalConfig) *configs.APIServer {
	enabled, _ := cfg.String("api.ssl.enabled")
	keystorePassword, _ := cfg.String("api.ssl.keystore.password")
	authType, _ := cfg.String("api.auth.type")
	username, _ := cfg.String("api.auth.basic.username")
	pw, _ := cfg.String("api.auth.basic.password")

	return &configs.APIServer{
		SSLEnabled:       enabled,
		KeystorePassword: keystorePassword,
		AuthType:         authType,
		Username:         username,
		Password:         pw,
	}
}

// patchWithPatternValue matches the pattern ${VAR:default_value} against config and assign the default value
// It gives a map of string (VAR) and config pointer that need to further resolve
// VAR is the variable name that expect to be defined in Keystore or Env
//
//	The variable name can consist of digit, underscores and letters
//	The default value is optional, ${VAR:} and ${VAR} are valid.
//
// config is updated with the default value
func patchWithPatternValue(config *configs.APIServer) map[string]*string {
	data := make(map[string]*string)

	pattern := `^\${([a-zA-Z0-9_]+)(?::(.*?))?}$`
	regex := regexp.MustCompile(pattern)

	for _, configKey := range []*string{&config.SSLEnabled, &config.KeystorePassword, &config.AuthType, &config.Username, &config.Password} {
		if match := regex.FindStringSubmatch(*configKey); match != nil {
			key := match[1]
			defaultValue := match[2]
			*configKey = defaultValue
			data[key] = configKey
		}
	}

	return data
}

// getKeystoreEnvKeyValue gives a map that consolidate all key value pairs from user defined environment variables
// and Keystore from SecureSettings. If the same key defined in both places, keystore takes the precedence.
func getKeystoreEnvKeyValue(params Params) (map[string]string, error) {
	data := make(map[string]string)
	c := getLogstashContainer(params.Logstash.Spec.PodTemplate.Spec.Containers)

	// from ENV
	for _, env := range c.Env {
		data[env.Name] = env.Value
	}

	for _, envFrom := range c.EnvFrom {
		// from ConfigMap
		if envFrom.ConfigMapRef != nil {
			configMap := corev1.ConfigMap{}
			nsn := types.NamespacedName{Name: envFrom.ConfigMapRef.LocalObjectReference.Name, Namespace: params.Logstash.Namespace}
			if err := params.Client.Get(params.Context, nsn, &configMap); err != nil {
				return nil, err
			}

			for key, value := range configMap.Data {
				data[key] = value
			}
		}

		// from Secret
		if envFrom.SecretRef != nil {
			secret := corev1.Secret{}
			nsn := types.NamespacedName{Name: envFrom.SecretRef.LocalObjectReference.Name, Namespace: params.Logstash.Namespace}
			if err := params.Client.Get(params.Context, nsn, &secret); err != nil {
				return nil, err
			}

			for key, value := range secret.Data {
				data[key] = string(value)
			}
		}
	}

	// from keystore SecureSettings
	for _, ss := range params.Logstash.SecureSettings() {
		secret := corev1.Secret{}
		nsn := types.NamespacedName{Name: ss.SecretName, Namespace: params.Logstash.Namespace}
		if err := params.Client.Get(params.Context, nsn, &secret); err != nil {
			return nil, err
		}

		for key, value := range secret.Data {
			data[key] = string(value)
		}
	}

	return data, nil
}

func getLogstashContainer(containers []corev1.Container) *corev1.Container {
	for _, c := range containers {
		if c.Name == logstashv1alpha1.LogstashContainerName {
			return &c
		}
	}
	return nil
}

// resolveConfigValue updates the configs with the actual values from Env or Keystore
func resolveConfigValue(unresolved map[string]*string, combinedMap map[string]string) {
	for varName, config := range unresolved {
		if actualValue, ok := combinedMap[varName]; ok {
			*config = actualValue
		}
	}
}
