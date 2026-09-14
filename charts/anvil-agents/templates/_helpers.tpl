{{- define "anvil-agents.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "anvil-agents.apiName" -}}
{{- $base := include "anvil-agents.name" . | trunc 59 | trimSuffix "-" -}}
{{- printf "%s-api" $base }}
{{- end }}

{{- define "anvil-agents.apiFullname" -}}
{{- $base := include "anvil-agents.fullname" . | trunc 59 | trimSuffix "-" -}}
{{- printf "%s-api" $base }}
{{- end }}

{{- define "anvil-agents.apiServiceAccountName" -}}
{{- if .Values.api.serviceAccount.create }}
{{- default (include "anvil-agents.apiFullname" .) .Values.api.serviceAccount.name }}
{{- else }}
{{- required "api.serviceAccount.name is required when api.serviceAccount.create=false" .Values.api.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "anvil-agents.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "anvil-agents.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "anvil-agents.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "anvil-agents.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create=false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Resolve the archive mode while preserving the pre-mode databaseURLSecret
contract. A legacy Secret name enables external mode only while mode remains
at its default value.
*/}}
{{- define "anvil-agents.archiveMode" -}}
{{- $mode := default "" .Values.archive.mode -}}
{{- if and (eq $mode "") .Values.archive.databaseURLSecret.name -}}
external
{{- else if eq $mode "" -}}
disabled
{{- else -}}
{{- $mode -}}
{{- end -}}
{{- end }}

{{- define "anvil-agents.archiveStandaloneName" -}}
{{- $base := include "anvil-agents.fullname" . | trunc 42 | trimSuffix "-" -}}
{{- printf "%s-archive-postgres" $base -}}
{{- end }}

{{- define "anvil-agents.archiveCloudNativePGName" -}}
{{- $base := include "anvil-agents.fullname" . | trunc 46 | trimSuffix "-" -}}
{{- default (printf "%s-archive-cnpg" $base) .Values.archive.cloudnativePG.clusterName -}}
{{- end }}

{{- define "anvil-agents.archiveSecretName" -}}
{{- $mode := include "anvil-agents.archiveMode" . -}}
{{- if eq $mode "external" -}}
  {{- default .Values.archive.databaseURLSecret.name .Values.archive.external.databaseURLSecret.name -}}
{{- else if eq $mode "standalone" -}}
  {{- default (include "anvil-agents.archiveStandaloneName" .) .Values.archive.standalone.auth.existingSecret -}}
{{- else if eq $mode "cloudnativepg" -}}
  {{- printf "%s-app" (include "anvil-agents.archiveCloudNativePGName" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{- define "anvil-agents.archiveSecretKey" -}}
{{- $mode := include "anvil-agents.archiveMode" . -}}
{{- if eq $mode "external" -}}
  {{- if .Values.archive.external.databaseURLSecret.name -}}
    {{- .Values.archive.external.databaseURLSecret.key -}}
  {{- else -}}
    {{- .Values.archive.databaseURLSecret.key -}}
  {{- end -}}
{{- else if eq $mode "standalone" -}}
  {{- .Values.archive.standalone.auth.databaseURLKey -}}
{{- else if eq $mode "cloudnativepg" -}}
uri
{{- end -}}
{{- end }}

{{- define "anvil-agents.validateArchive" -}}
{{- $mode := include "anvil-agents.archiveMode" . -}}
{{- $allowed := list "disabled" "external" "standalone" "cloudnativepg" -}}
{{- if not (has $mode $allowed) -}}
  {{- fail (printf "archive.mode must be one of disabled, external, standalone, or cloudnativepg; got %q" $mode) -}}
{{- end -}}
{{- if and .Values.archive.databaseURLSecret.name .Values.archive.external.databaseURLSecret.name -}}
  {{- fail "archive.databaseURLSecret and archive.external.databaseURLSecret cannot both be configured" -}}
{{- end -}}
{{- $configuredMode := default "" .Values.archive.mode -}}
{{- if and .Values.archive.databaseURLSecret.name (not (or (eq $configuredMode "") (eq $configuredMode "external"))) -}}
  {{- fail "archive.databaseURLSecret is a deprecated external-mode alias and cannot be combined with another archive mode" -}}
{{- end -}}
{{- if and .Values.archive.external.databaseURLSecret.name (ne $mode "external") -}}
  {{- fail "archive.external.databaseURLSecret can be configured only in external mode" -}}
{{- end -}}
{{- if and (or .Values.archive.standalone.auth.existingSecret .Values.archive.standalone.auth.generate) (ne $mode "standalone") -}}
  {{- fail "archive.standalone.auth can be configured only in standalone mode" -}}
{{- end -}}
{{- if and (eq $mode "disabled") .Values.archive.terminalRetention -}}
  {{- fail "archive.terminalRetention requires an enabled PostgreSQL archive mode" -}}
{{- end -}}
{{- if and .Values.archive.terminalRetention (not (regexMatch "^([0-9]+(ns|us|ms|s|m|h))+$" .Values.archive.terminalRetention)) -}}
  {{- fail "archive.terminalRetention must be a positive Go duration such as 24h or 30m" -}}
{{- end -}}
{{- if eq $mode "external" -}}
  {{- $_ := required "archive.external.databaseURLSecret.name is required for external mode" (include "anvil-agents.archiveSecretName" .) -}}
  {{- $_ := required "archive.external.databaseURLSecret.key is required for external mode" (include "anvil-agents.archiveSecretKey" .) -}}
  {{- $secretName := include "anvil-agents.archiveSecretName" . -}}
  {{- if or (gt (len $secretName) 253) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" $secretName)) -}}
    {{- fail "archive external Secret name must be a valid DNS subdomain" -}}
  {{- end -}}
{{- else if eq $mode "standalone" -}}
  {{- if and .Values.archive.standalone.auth.existingSecret .Values.archive.standalone.auth.generate -}}
    {{- fail "archive.standalone.auth.existingSecret and archive.standalone.auth.generate are mutually exclusive" -}}
  {{- end -}}
  {{- if not (or .Values.archive.standalone.auth.existingSecret .Values.archive.standalone.auth.generate) -}}
    {{- fail "archive.mode=standalone requires archive.standalone.auth.existingSecret or explicit archive.standalone.auth.generate=true" -}}
  {{- end -}}
  {{- with .Values.archive.standalone.auth.existingSecret -}}
    {{- if or (gt (len .) 253) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" .)) -}}
      {{- fail "archive.standalone.auth.existingSecret must be a valid DNS subdomain" -}}
    {{- end -}}
  {{- end -}}
  {{- if not (regexMatch "^[A-Za-z_][A-Za-z0-9_]*$" .Values.archive.standalone.username) -}}
    {{- fail "archive.standalone.username must be a PostgreSQL identifier containing only letters, numbers, and underscores" -}}
  {{- end -}}
  {{- if or (gt (len .Values.archive.standalone.username) 63) (gt (len .Values.archive.standalone.database) 63) -}}
    {{- fail "archive.standalone username and database must be at most 63 characters" -}}
  {{- end -}}
  {{- if not (regexMatch "^[A-Za-z_][A-Za-z0-9_]*$" .Values.archive.standalone.database) -}}
    {{- fail "archive.standalone.database must be a PostgreSQL identifier containing only letters, numbers, and underscores" -}}
  {{- end -}}
  {{- $_ := required "archive.standalone.auth.passwordKey is required" .Values.archive.standalone.auth.passwordKey -}}
  {{- $_ := required "archive.standalone.auth.databaseURLKey is required" .Values.archive.standalone.auth.databaseURLKey -}}
  {{- if or (not (regexMatch "^[-._A-Za-z0-9]+$" .Values.archive.standalone.auth.passwordKey)) (not (regexMatch "^[-._A-Za-z0-9]+$" .Values.archive.standalone.auth.databaseURLKey)) -}}
    {{- fail "archive.standalone Secret keys may contain only letters, numbers, dash, underscore, and dot" -}}
  {{- end -}}
  {{- if eq .Values.archive.standalone.auth.passwordKey .Values.archive.standalone.auth.databaseURLKey -}}
    {{- fail "archive.standalone.auth.passwordKey and databaseURLKey must be different" -}}
  {{- end -}}
  {{- $_ := required "archive.standalone.storage.size is required" .Values.archive.standalone.storage.size -}}
{{- else if eq $mode "cloudnativepg" -}}
  {{- if not (.Capabilities.APIVersions.Has "postgresql.cnpg.io/v1/Cluster") -}}
    {{- fail "archive.mode=cloudnativepg requires the CloudNativePG postgresql.cnpg.io/v1 Cluster CRD; install the operator separately" -}}
  {{- end -}}
  {{- if lt (int .Values.archive.cloudnativePG.instances) 1 -}}
    {{- fail "archive.cloudnativePG.instances must be at least 1" -}}
  {{- end -}}
  {{- $clusterName := include "anvil-agents.archiveCloudNativePGName" . -}}
  {{- if or (gt (len $clusterName) 59) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $clusterName)) -}}
    {{- fail "archive.cloudnativePG.clusterName must be a DNS label of at most 59 characters so CloudNativePG can create its application Secret" -}}
  {{- end -}}
  {{- $_ := required "archive.cloudnativePG.storage.size is required" .Values.archive.cloudnativePG.storage.size -}}
{{- end -}}
{{- end }}

{{/*
Controller-owned Gateway API HTTPRoute for AgentExternalTrigger webhooks.
Enabled when the API, externalTriggers, and either api.httpRoute or a
dedicated externalTriggerHTTPRoute.enabled flag is on. Empty dedicated
parentRefs/hostnames inherit api.httpRoute.
*/}}
{{- define "anvil-agents.externalTriggerHTTPRouteEnabled" -}}
{{- $ext := dig "externalTriggers" "enabled" false (.Values.api.config | default dict) -}}
{{- $dedicated := dig "enabled" false (.Values.api.externalTriggerHTTPRoute | default dict) -}}
{{- if and .Values.api.enabled $ext (or $dedicated .Values.api.httpRoute.enabled) -}}
true
{{- end -}}
{{- end }}

{{- define "anvil-agents.externalTriggerHTTPRouteNamespace" -}}
{{- $dedicated := .Values.api.externalTriggerHTTPRoute | default dict -}}
{{- $ns := index $dedicated "namespace" | default "" | toString | trim -}}
{{- if $ns -}}
{{- $ns -}}
{{- else -}}
{{- .Release.Namespace -}}
{{- end -}}
{{- end }}

{{- define "anvil-agents.externalTriggerHTTPRouteConfig" -}}
{{- $dedicated := .Values.api.externalTriggerHTTPRoute | default dict -}}
{{- $parents := index $dedicated "parentRefs" | default list -}}
{{- if not $parents -}}
{{- $parents = .Values.api.httpRoute.parentRefs | default list -}}
{{- end -}}
{{- $hosts := index $dedicated "hostnames" | default list -}}
{{- if not $hosts -}}
{{- $hosts = .Values.api.httpRoute.hostnames | default list -}}
{{- end -}}
{{- dict
  "parentRefs" $parents
  "hostnames" $hosts
  "backendName" (include "anvil-agents.apiFullname" .)
  "backendNamespace" .Release.Namespace
  "backendPort" (.Values.api.service.port | int)
  "routeNamespace" (include "anvil-agents.externalTriggerHTTPRouteNamespace" .)
  | toJson -}}
{{- end }}

{{- define "anvil-agents.validateExternalTriggerHTTPRoute" -}}
{{- if eq (include "anvil-agents.externalTriggerHTTPRouteEnabled" . | trim) "true" -}}
{{- $cfg := include "anvil-agents.externalTriggerHTTPRouteConfig" . | fromJson -}}
{{- $parents := index $cfg "parentRefs" -}}
{{- $hosts := index $cfg "hostnames" -}}
{{- if not $parents -}}
{{- fail "external trigger HTTPRoute parentRefs must contain at least one Gateway parent (set api.externalTriggerHTTPRoute.parentRefs or inherit api.httpRoute.parentRefs)" -}}
{{- end -}}
{{- if not $hosts -}}
{{- fail "external trigger HTTPRoute hostnames must contain an explicit hostname (set api.externalTriggerHTTPRoute.hostnames or inherit api.httpRoute.hostnames)" -}}
{{- end -}}
{{- range $hostname := $hosts -}}
{{- if or (contains "*" $hostname) (not (regexMatch "^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$" $hostname)) -}}
{{- fail "external trigger HTTPRoute hostnames must contain exact hostnames, never wildcards" -}}
{{- end -}}
{{- end -}}
{{- range $parent := $parents -}}
{{- if not $parent.sectionName -}}
{{- fail "each external trigger HTTPRoute parentRefs entry must select an explicit HTTPS listener with sectionName" -}}
{{- end -}}
{{- if not $parent.name -}}
{{- fail "each external trigger HTTPRoute parentRefs entry must set name" -}}
{{- end -}}
{{- end -}}
{{- $routeNS := include "anvil-agents.externalTriggerHTTPRouteNamespace" . | trim -}}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $routeNS) -}}
{{- fail "external trigger HTTPRoute namespace must be a valid DNS label (api.externalTriggerHTTPRoute.namespace or the Helm release namespace)" -}}
{{- end -}}
{{- end -}}
{{- end }}
