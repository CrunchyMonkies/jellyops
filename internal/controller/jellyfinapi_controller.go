/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	jellyfinv1alpha1 "github.com/crunchymonkies/jellyops/api/v1alpha1"
	"github.com/crunchymonkies/jellyops/internal/jellyfinapi"
)

const (
	defaultAPIRequeue = 5 * time.Minute
	apiKeyAppName     = "JellyOps"
)

// APIClient is the subset of the Jellyfin HTTP client the reconciler uses. It is
// an interface so tests can inject a fake without a running Jellyfin.
type APIClient interface {
	SetToken(string)
	Bootstrap(ctx context.Context, username, password, app string) (string, error)
	AuthenticateByName(ctx context.Context, username, password string) (string, error)
	ListVirtualFolders(ctx context.Context) ([]jellyfinapi.VirtualFolder, error)
	AddVirtualFolder(ctx context.Context, lib jellyfinapi.DesiredLibrary, refresh bool) error
	RemoveVirtualFolder(ctx context.Context, name string, refresh bool) error
	AddMediaPath(ctx context.Context, name, path string, refresh bool) error
	RemoveMediaPath(ctx context.Context, name, path string, refresh bool) error
	UpdateLibraryOptions(ctx context.Context, id string, options json.RawMessage) error
	RefreshLibraries(ctx context.Context) error
	GetEncodingConfig(ctx context.Context) (json.RawMessage, error)
	UpdateEncodingConfig(ctx context.Context, cfg json.RawMessage) error
	GetServerConfig(ctx context.Context) (json.RawMessage, error)
	UpdateServerConfig(ctx context.Context, cfg json.RawMessage) error
	GetBrandingConfig(ctx context.Context) (json.RawMessage, error)
	UpdateBrandingConfig(ctx context.Context, cfg json.RawMessage) error
}

// JellyfinAPIReconciler is a day-2 loop that authenticates to a Ready instance
// and reconciles in-app state (libraries) over the Jellyfin HTTP API.
type JellyfinAPIReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// NewAPIClient builds a client for the given base URL and device id. Defaults
	// to the real jellyfinapi client; tests override it.
	NewAPIClient func(baseURL, deviceID string) (APIClient, error)
	// RequeueInterval controls how often the loop re-checks in-app state.
	RequeueInterval time.Duration
}

// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfins,verbs=get;list;watch
// +kubebuilder:rbac:groups=jellyfin.jellyops.io,resources=jellyfins/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch

// Reconcile drives bootstrap + library reconciliation for a Ready instance.
func (r *JellyfinAPIReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var jf jellyfinv1alpha1.Jellyfin
	if err := r.Get(ctx, req.NamespacedName, &jf); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if jf.Spec.API == nil || !jf.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	requeue := r.RequeueInterval
	if requeue == 0 {
		requeue = defaultAPIRequeue
	}

	// Gate on instance readiness; never address the public Ingress.
	if c := apimeta.FindStatusCondition(jf.Status.Conditions, conditionReady); c == nil || c.Status != metav1.ConditionTrue {
		setCondition(&jf.Status.Conditions, conditionAPIReady, metav1.ConditionFalse, "InstanceNotReady", "Waiting for instance to become Ready", jf.Generation)
		return ctrl.Result{RequeueAfter: requeue}, writeJellyfinStatus(ctx, r.Client, &jf)
	}

	base := jf.Status.Endpoints.Service
	if base == "" {
		base = fmt.Sprintf("http://%s.%s.svc:%d", jf.Name, jf.Namespace, jellyfinPort(&jf))
	}
	cli, err := r.newClient(base, string(jf.UID))
	if err != nil {
		return ctrl.Result{}, err
	}

	token, err := r.ensureCredentials(ctx, &jf, cli)
	if err != nil {
		log.Error(err, "credential resolution failed")
		setCondition(&jf.Status.Conditions, conditionAPIReady, metav1.ConditionFalse, "AuthFailed", err.Error(), jf.Generation)
		return ctrl.Result{RequeueAfter: requeue}, writeJellyfinStatus(ctx, r.Client, &jf)
	}
	cli.SetToken(token)

	if jf.Spec.API.ManageLibraries {
		if err := r.reconcileLibraries(ctx, &jf, cli); err != nil {
			log.Error(err, "library reconciliation failed")
			setCondition(&jf.Status.Conditions, conditionLibrariesReady, metav1.ConditionFalse, "LibraryError", err.Error(), jf.Generation)
			setCondition(&jf.Status.Conditions, conditionAPIReady, metav1.ConditionTrue, "Authenticated", "Authenticated; library reconcile pending", jf.Generation)
			return ctrl.Result{RequeueAfter: requeue}, writeJellyfinStatus(ctx, r.Client, &jf)
		}
		setCondition(&jf.Status.Conditions, conditionLibrariesReady, metav1.ConditionTrue, "Reconciled", "Libraries reconciled", jf.Generation)
	}

	if jf.Spec.Transcoding != nil {
		if err := r.reconcileEncoding(ctx, &jf, cli); err != nil {
			log.Error(err, "encoding reconciliation failed")
			setCondition(&jf.Status.Conditions, conditionTranscodingReady, metav1.ConditionFalse, "EncodingError", err.Error(), jf.Generation)
			setCondition(&jf.Status.Conditions, conditionAPIReady, metav1.ConditionTrue, "Authenticated", "Authenticated; encoding reconcile pending", jf.Generation)
			return ctrl.Result{RequeueAfter: requeue}, writeJellyfinStatus(ctx, r.Client, &jf)
		}
		setCondition(&jf.Status.Conditions, conditionTranscodingReady, metav1.ConditionTrue, "Reconciled", "Transcode throttling/segment deletion reconciled", jf.Generation)
	}

	if jf.Spec.General != nil || jf.Spec.Playback != nil {
		if err := r.reconcileServerConfig(ctx, &jf, cli); err != nil {
			log.Error(err, "server configuration reconciliation failed")
			setCondition(&jf.Status.Conditions, conditionServerConfigReady, metav1.ConditionFalse, "ServerConfigError", err.Error(), jf.Generation)
			setCondition(&jf.Status.Conditions, conditionAPIReady, metav1.ConditionTrue, "Authenticated", "Authenticated; server config reconcile pending", jf.Generation)
			return ctrl.Result{RequeueAfter: requeue}, writeJellyfinStatus(ctx, r.Client, &jf)
		}
		setCondition(&jf.Status.Conditions, conditionServerConfigReady, metav1.ConditionTrue, "Reconciled", "General/Playback settings reconciled", jf.Generation)
	}

	if jf.Spec.Branding != nil {
		if err := r.reconcileBranding(ctx, &jf, cli); err != nil {
			log.Error(err, "branding reconciliation failed")
			setCondition(&jf.Status.Conditions, conditionBrandingReady, metav1.ConditionFalse, "BrandingError", err.Error(), jf.Generation)
			setCondition(&jf.Status.Conditions, conditionAPIReady, metav1.ConditionTrue, "Authenticated", "Authenticated; branding reconcile pending", jf.Generation)
			return ctrl.Result{RequeueAfter: requeue}, writeJellyfinStatus(ctx, r.Client, &jf)
		}
		setCondition(&jf.Status.Conditions, conditionBrandingReady, metav1.ConditionTrue, "Reconciled", "Branding settings reconciled", jf.Generation)
	}

	setCondition(&jf.Status.Conditions, conditionAPIReady, metav1.ConditionTrue, "Authenticated", "Authenticated to the Jellyfin API", jf.Generation)
	return ctrl.Result{RequeueAfter: requeue}, writeJellyfinStatus(ctx, r.Client, &jf)
}

func (r *JellyfinAPIReconciler) newClient(base, deviceID string) (APIClient, error) {
	if r.NewAPIClient != nil {
		return r.NewAPIClient(base, deviceID)
	}
	return jellyfinapi.New(base, deviceID, &http.Client{Timeout: 30 * time.Second})
}

// ensureCredentials resolves an API token per spec.api.mode, bootstrapping and
// persisting a key when needed. The token itself is never written to status.
func (r *JellyfinAPIReconciler) ensureCredentials(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, cli APIClient) (string, error) {
	api := jf.Spec.API

	// Read optional admin credentials.
	var user, pass, providedKey string
	if api.CredentialsSecret != nil && api.CredentialsSecret.Name != "" {
		sec, err := r.getSecret(ctx, jf.Namespace, api.CredentialsSecret.Name)
		if err != nil {
			return "", fmt.Errorf("read credentialsSecret: %w", err)
		}
		providedKey = string(sec.Data["apiKey"])
		user = string(sec.Data["username"])
		pass = string(sec.Data["password"])
	}

	if api.Mode == "provided" {
		if providedKey != "" {
			return providedKey, nil
		}
		if user == "" || pass == "" {
			return "", fmt.Errorf("mode=provided requires apiKey or username+password in credentialsSecret")
		}
		return cli.AuthenticateByName(ctx, user, pass)
	}

	// Bootstrap mode: reuse a previously-minted key if it still works.
	genName := api.GeneratedSecretName
	if genName == "" {
		genName = jf.Name + "-api"
	}
	if gsec, err := r.getSecret(ctx, jf.Namespace, genName); err == nil {
		if key := string(gsec.Data["apiKey"]); key != "" {
			cli.SetToken(key)
			if _, err := cli.ListVirtualFolders(ctx); err == nil {
				jf.Status.APICredentialsSecret = genName
				return key, nil
			}
		}
		if user == "" {
			user = string(gsec.Data["username"])
		}
		if pass == "" {
			pass = string(gsec.Data["password"])
		}
	}

	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = randomSecret()
	}

	key, err := cli.Bootstrap(ctx, user, pass, apiKeyAppName)
	if err != nil {
		return "", err
	}
	if err := r.persistGeneratedSecret(ctx, jf, genName, user, pass, key); err != nil {
		return "", err
	}
	jf.Status.APICredentialsSecret = genName
	return key, nil
}

func (r *JellyfinAPIReconciler) reconcileLibraries(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, cli APIClient) error {
	desired := desiredLibraries(jf)
	existing, err := cli.ListVirtualFolders(ctx)
	if err != nil {
		return err
	}
	refresh := jf.Spec.API.RefreshLibraryOnChange
	diff := jellyfinapi.DiffLibraries(desired, existing, jf.Status.ManagedLibraries, jf.Spec.API.Prune)

	for _, lib := range diff.ToCreate {
		if err := cli.AddVirtualFolder(ctx, lib, false); err != nil {
			return err
		}
	}
	for name, paths := range diff.PathsToAdd {
		for _, p := range paths {
			if err := cli.AddMediaPath(ctx, name, p, false); err != nil {
				return err
			}
		}
	}
	for name, paths := range diff.PathsToRemove {
		for _, p := range paths {
			if err := cli.RemoveMediaPath(ctx, name, p, false); err != nil {
				return err
			}
		}
	}
	for _, name := range diff.ToRemove {
		if err := cli.RemoveVirtualFolder(ctx, name, false); err != nil {
			return err
		}
	}

	// Enforce write-disabling options for read-only media libraries so Jellyfin
	// never writes .nfo/subtitles into a read-only mount. Re-list after creates so
	// newly-created libraries are included with their full (default) options.
	folders := existing
	if len(diff.ToCreate) > 0 {
		if folders, err = cli.ListVirtualFolders(ctx); err != nil {
			return err
		}
	}
	byName := make(map[string]jellyfinapi.VirtualFolder, len(folders))
	for _, f := range folders {
		byName[f.Name] = f
	}
	optsChanged := false
	for _, d := range desired {
		if !d.PreventWrites {
			continue
		}
		f, ok := byName[d.Name]
		if !ok || f.ItemID == "" {
			continue
		}
		updated, changed, err := jellyfinapi.EnforceReadOnlyOptions(f.LibraryOptions)
		if err != nil {
			return err
		}
		if changed {
			if err := cli.UpdateLibraryOptions(ctx, f.ItemID, updated); err != nil {
				return err
			}
			optsChanged = true
		}
	}

	if (diff.Changed() || optsChanged) && refresh {
		if err := cli.RefreshLibraries(ctx); err != nil {
			return err
		}
	}

	names := make([]string, 0, len(desired))
	for _, d := range desired {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	jf.Status.ManagedLibraries = names
	return nil
}

// desiredLibraries derives the intended libraries from media folders that carry
// a library block.
func desiredLibraries(jf *jellyfinv1alpha1.Jellyfin) []jellyfinapi.DesiredLibrary {
	var out []jellyfinapi.DesiredLibrary
	for _, mf := range jf.Spec.Storage.Media {
		if mf.Library == nil {
			continue
		}
		name := mf.Library.Name
		if name == "" {
			name = mf.Name
		}
		lib := jellyfinapi.DesiredLibrary{
			Name:           name,
			CollectionType: mf.Library.CollectionType,
			Paths:          []string{mf.MountPath},
			PreventWrites:  mf.ReadOnly,
		}
		if mf.Library.Options != nil {
			lib.Options = mf.Library.Options.Raw
		}
		out = append(out, lib)
	}
	return out
}

// reconcileEncoding applies the managed transcode-cache fields onto the instance's
// encoding config. It GETs the current config, overlays only the fields declared in
// spec.transcoding (preserving QSV/VAAPI and any other settings), and POSTs back
// only when something changed.
func (r *JellyfinAPIReconciler) reconcileEncoding(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, cli APIClient) error {
	desired := desiredEncoding(jf.Spec.Transcoding)
	if desired.Empty() {
		return nil
	}
	current, err := cli.GetEncodingConfig(ctx)
	if err != nil {
		return err
	}
	updated, changed, err := jellyfinapi.EnforceEncodingOptions(current, desired)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return cli.UpdateEncodingConfig(ctx, updated)
}

// desiredEncoding maps the CR's transcoding block to the managed encoding fields.
// Unset pointers stay nil so the operator never overwrites a field the user did not
// declare.
func desiredEncoding(t *jellyfinv1alpha1.TranscodingSpec) jellyfinapi.DesiredEncoding {
	var d jellyfinapi.DesiredEncoding
	if t == nil {
		return d
	}
	if t.Throttle != nil {
		d.EnableThrottling = t.Throttle.Enabled
		d.ThrottleDelaySeconds = t.Throttle.DelaySeconds
	}
	if t.SegmentDeletion != nil {
		d.EnableSegmentDeletion = t.SegmentDeletion.Enabled
		d.SegmentKeepSeconds = t.SegmentDeletion.KeepSeconds
	}
	return d
}

// reconcileServerConfig applies the managed General + Playback fields onto the
// instance's root ServerConfiguration. It GETs the current config, overlays only the
// fields declared in spec.general / spec.playback (preserving everything else), and
// POSTs the full object back only when something changed.
func (r *JellyfinAPIReconciler) reconcileServerConfig(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, cli APIClient) error {
	desired := desiredServerConfig(jf.Spec.General, jf.Spec.Playback)
	if desired.Empty() {
		return nil
	}
	current, err := cli.GetServerConfig(ctx)
	if err != nil {
		return err
	}
	updated, changed, err := jellyfinapi.EnforceServerConfig(current, desired)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return cli.UpdateServerConfig(ctx, updated)
}

// desiredServerConfig maps the CR's general + playback blocks to the managed
// ServerConfiguration fields. Unset pointers stay nil so the operator never overwrites
// a field the user did not declare.
func desiredServerConfig(g *jellyfinv1alpha1.GeneralSpec, p *jellyfinv1alpha1.PlaybackSpec) jellyfinapi.DesiredServerConfig {
	var d jellyfinapi.DesiredServerConfig
	if g != nil {
		d.ServerName = g.ServerName
		d.UICulture = g.UICulture
		d.QuickConnectAvailable = g.QuickConnectAvailable
		d.EnableMetrics = g.EnableMetrics
		d.EnableNormalizedItemByNameIds = g.EnableNormalizedItemByNameIds
		d.AllowClientLogUpload = g.AllowClientLogUpload
		d.EnableSlowResponseWarning = g.EnableSlowResponseWarning
		d.SlowResponseThresholdMs = g.SlowResponseThresholdMs
		d.LibraryScanFanoutConcurrency = g.LibraryScanFanoutConcurrency
		d.LibraryMetadataRefreshConcurrency = g.LibraryMetadataRefreshConcurrency
		d.ParallelImageEncodingLimit = g.ParallelImageEncodingLimit
		d.ActivityLogRetentionDays = g.ActivityLogRetentionDays
		d.LibraryMonitorDelay = g.LibraryMonitorDelay
		d.LibraryUpdateDuration = g.LibraryUpdateDuration
		d.InactiveSessionThreshold = g.InactiveSessionThreshold
		d.LogFileRetentionDays = g.LogFileRetentionDays
		d.CachePath = g.CachePath
		d.MetadataPath = g.MetadataPath
		d.CorsHosts = g.CorsHosts
	}
	if p != nil {
		d.MinResumePct = p.MinResumePct
		d.MaxResumePct = p.MaxResumePct
		d.MinResumeDurationSeconds = p.MinResumeDurationSeconds
		d.MinAudiobookResume = p.MinAudiobookResumeMinutes
		d.MaxAudiobookResume = p.MaxAudiobookResumeMinutes
		d.RemoteClientBitrateLimit = p.RemoteClientBitrateLimit
	}
	return d
}

// reconcileBranding applies the managed branding fields onto the instance's branding
// config. GET, overlay declared fields (preserving SplashscreenLocation), POST back
// only when something changed.
func (r *JellyfinAPIReconciler) reconcileBranding(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, cli APIClient) error {
	desired := desiredBranding(jf.Spec.Branding)
	if desired.Empty() {
		return nil
	}
	current, err := cli.GetBrandingConfig(ctx)
	if err != nil {
		return err
	}
	updated, changed, err := jellyfinapi.EnforceBranding(current, desired)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return cli.UpdateBrandingConfig(ctx, updated)
}

// desiredBranding maps the CR's branding block to the managed branding fields.
func desiredBranding(b *jellyfinv1alpha1.BrandingSpec) jellyfinapi.DesiredBranding {
	var d jellyfinapi.DesiredBranding
	if b == nil {
		return d
	}
	d.LoginDisclaimer = b.LoginDisclaimer
	d.CustomCss = b.CustomCss
	d.SplashscreenEnabled = b.SplashscreenEnabled
	return d
}

func (r *JellyfinAPIReconciler) getSecret(ctx context.Context, ns, name string) (*corev1.Secret, error) {
	var sec corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &sec); err != nil {
		return nil, err
	}
	return &sec, nil
}

func (r *JellyfinAPIReconciler) persistGeneratedSecret(ctx context.Context, jf *jellyfinv1alpha1.Jellyfin, name, user, pass, key string) error {
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: jf.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sec, func() error {
		if sec.Data == nil {
			sec.Data = map[string][]byte{}
		}
		sec.Data["apiKey"] = []byte(key)
		sec.Data["username"] = []byte(user)
		sec.Data["password"] = []byte(pass)
		return controllerutil.SetControllerReference(jf, sec, r.Scheme)
	})
	return err
}

func jellyfinPort(jf *jellyfinv1alpha1.Jellyfin) int32 {
	if jf.Spec.Service.Port != 0 {
		return jf.Spec.Service.Port
	}
	return 8096
}

func randomSecret() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		// rand.Read never fails on Linux; fall back to a fixed-length marker.
		return "changeme-jellyops"
	}
	return hex.EncodeToString(b)
}

// SetupWithManager wires this as a second controller on the Jellyfin kind, with
// a distinct name so registration does not collide with JellyfinReconciler.
func (r *JellyfinAPIReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&jellyfinv1alpha1.Jellyfin{}).
		Owns(&corev1.Secret{}).
		Named("jellyfinapi").
		Complete(r)
}
