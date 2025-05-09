{{- define "imageSpec" -}}
{{- $v := index . 0 -}}
{{- $container := index . 1 -}}
{{- $cv := get $v $container -}}
{{ printf "%s/%s:%s" ($cv.registry | default $v.registry) $cv.repo $cv.tag | quote }}
{{- end -}}

{{- define "workerImageSpec" -}}
{{- $v := index . 0 -}}
{{- $container := index . 1 -}}
{{- $cv := get $v $container -}}
{{ printf "%s/%s:%s" ($cv.registry | default $v.workerRegistry | default $v.registry) $cv.repo $cv.tag | quote }}
{{- end -}}

{{- define "enabledPlugins" -}}
    {{- $drivers := list -}}
    {{- $csi := . -}}
    {{- range $key := tuple "disk" "nas" "oss" }}
        {{- if (index $csi $key).enabled -}}
            {{- $drivers = append $drivers $key -}}
        {{- end -}}
    {{- end -}}
    {{- $drivers | join "," -}}
{{- end -}}

{{- define "enabledControllers" -}}
    {{- $drivers := list -}}
    {{- $csi := . -}}
    {{- range $key := tuple "disk" "nas" "oss" }}
        {{- $val := index $csi $key -}}
        {{- if and $val.enabled $val.controller.enabled -}}
            {{- $drivers = append $drivers $key -}}
        {{- end -}}
    {{- end -}}
    {{- $drivers | join "," -}}
{{- end -}}

{{- define "akEnv" -}}
{{- if .enabled -}}
- name: ACCESS_KEY_ID
  valueFrom:
    secretKeyRef:
      name: {{ .secretName }}
      key: {{ default "id" .idKey }}
- name: ACCESS_KEY_SECRET
  valueFrom:
    secretKeyRef:
      name: {{ .secretName }}
      key: {{ default "secret" .secretKey }}
{{- end -}}
{{- end -}}

{{- define "kubeletDirEnv" -}}
{{- $d := clean . -}}
{{- if ne $d "/var/lib/kubelet" -}}
- name: KUBELET_ROOT_DIR
  value: {{ $d | quote }}
{{- end -}}
{{- end -}}
