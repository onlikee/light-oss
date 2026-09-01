package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type openAPIRouteKey struct {
	Method string
	Path   string
}

func TestOpenAPISpecsMatchRegisteredRoutes(t *testing.T) {
	router := newTestRouter(t, 1024)
	wantRoutes := make(map[openAPIRouteKey]struct{})
	for _, route := range router.Routes() {
		wantRoutes[openAPIRouteKey{
			Method: route.Method,
			Path:   normalizeGinPathForOpenAPI(route.Path),
		}] = struct{}{}
	}

	for _, specPath := range []string{
		filepath.Join("..", "..", "docs", "openapi.apifox.json"),
		filepath.Join("..", "..", "docs", "openapi.apifox.cn.json"),
	} {
		specPath := specPath
		t.Run(filepath.Base(specPath), func(t *testing.T) {
			gotRoutes := loadOpenAPIRoutes(t, specPath)
			missing := routeSetDifference(wantRoutes, gotRoutes)
			extra := routeSetDifference(gotRoutes, wantRoutes)
			if len(missing) > 0 || len(extra) > 0 {
				t.Fatalf(
					"OpenAPI routes differ from registered Gin routes\nmissing from spec:\n%s\nextra in spec:\n%s",
					formatOpenAPIRoutes(missing),
					formatOpenAPIRoutes(extra),
				)
			}
			assertOpenAPIRouteSecurity(t, specPath, gotRoutes)
			assertOpenAPIManifestLimits(t, specPath)
			assertOpenAPICompleteness(t, specPath)
		})
	}

	assertOpenAPISpecsStructurallyEquivalent(
		t,
		filepath.Join("..", "..", "docs", "openapi.apifox.json"),
		filepath.Join("..", "..", "docs", "openapi.apifox.cn.json"),
	)
}

func assertOpenAPIManifestLimits(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenAPI spec %s: %v", path, err)
	}
	var document struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Type     string `json:"type"`
					MinItems int    `json:"minItems"`
					MaxItems int    `json:"maxItems"`
					Items    struct {
						Ref string `json:"$ref"`
					} `json:"items"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse OpenAPI spec %s: %v", path, err)
	}

	for _, schemaName := range []string{"UploadBatchForm", "PublishSiteForm"} {
		manifest := document.Components.Schemas[schemaName].Properties["manifest"]
		if manifest.Type != "array" {
			t.Errorf("OpenAPI schema %s manifest type = %q, want array", schemaName, manifest.Type)
		}
		if manifest.MinItems != 1 {
			t.Errorf("OpenAPI schema %s manifest minItems = %d, want 1", schemaName, manifest.MinItems)
		}
		if manifest.MaxItems != 2000 {
			t.Errorf("OpenAPI schema %s manifest maxItems = %d, want 2000", schemaName, manifest.MaxItems)
		}
		if manifest.Items.Ref != "#/components/schemas/UploadBatchManifestItem" {
			t.Errorf("OpenAPI schema %s manifest items = %q, want UploadBatchManifestItem", schemaName, manifest.Items.Ref)
		}
	}
}

func assertOpenAPIRouteSecurity(
	t *testing.T,
	path string,
	routes map[openAPIRouteKey]struct{},
) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenAPI spec %s: %v", path, err)
	}
	var document struct {
		Security json.RawMessage                       `json:"security"`
		Paths    map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse OpenAPI spec %s: %v", path, err)
	}

	for route := range routes {
		operationData := document.Paths[route.Path][strings.ToLower(route.Method)]
		var operation struct {
			Security json.RawMessage `json:"security"`
		}
		if err := json.Unmarshal(operationData, &operation); err != nil {
			t.Fatalf("parse OpenAPI operation %s %s: %v", route.Method, route.Path, err)
		}
		securityData := operation.Security
		if securityData == nil {
			securityData = document.Security
		}
		var requirements []map[string][]string
		if securityData != nil {
			if err := json.Unmarshal(securityData, &requirements); err != nil {
				t.Fatalf("parse OpenAPI security for %s %s: %v", route.Method, route.Path, err)
			}
		}

		if isPublicOpenAPIRoute(route) {
			if len(requirements) != 0 {
				t.Errorf("public OpenAPI operation %s %s unexpectedly requires security", route.Method, route.Path)
			}
			continue
		}
		if !hasOpenAPISecurityScheme(requirements, "bearerAuth") {
			t.Errorf("protected OpenAPI operation %s %s does not require bearerAuth", route.Method, route.Path)
		}
	}
}

func isPublicOpenAPIRoute(route openAPIRouteKey) bool {
	publicRoutes := map[openAPIRouteKey]struct{}{
		{Method: http.MethodGet, Path: "/livez"}:                                  {},
		{Method: http.MethodGet, Path: "/readyz"}:                                 {},
		{Method: http.MethodGet, Path: "/sites/{siteID}"}:                         {},
		{Method: http.MethodHead, Path: "/sites/{siteID}"}:                        {},
		{Method: http.MethodGet, Path: "/sites/{siteID}/{path}"}:                  {},
		{Method: http.MethodHead, Path: "/sites/{siteID}/{path}"}:                 {},
		{Method: http.MethodGet, Path: "/api/v1/buckets/{bucket}/objects/{key}"}:  {},
		{Method: http.MethodHead, Path: "/api/v1/buckets/{bucket}/objects/{key}"}: {},
	}
	_, public := publicRoutes[route]
	return public
}

func hasOpenAPISecurityScheme(requirements []map[string][]string, scheme string) bool {
	for _, requirement := range requirements {
		if _, exists := requirement[scheme]; exists {
			return true
		}
	}
	return false
}

func loadOpenAPIRoutes(t *testing.T, path string) map[openAPIRouteKey]struct{} {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenAPI spec %s: %v", path, err)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse OpenAPI spec %s: %v", path, err)
	}

	routes := make(map[openAPIRouteKey]struct{})
	for path, pathItem := range document.Paths {
		for method := range pathItem {
			upperMethod := strings.ToUpper(method)
			if !isOpenAPIOperationMethod(upperMethod) {
				continue
			}
			routes[openAPIRouteKey{Method: upperMethod, Path: path}] = struct{}{}
		}
	}
	return routes
}

func normalizeGinPathForOpenAPI(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			segments[index] = "{" + segment[1:] + "}"
		}
	}
	return strings.Join(segments, "/")
}

func isOpenAPIOperationMethod(method string) bool {
	switch method {
	case http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodOptions,
		http.MethodTrace:
		return true
	default:
		return false
	}
}

func routeSetDifference(
	left map[openAPIRouteKey]struct{},
	right map[openAPIRouteKey]struct{},
) []openAPIRouteKey {
	difference := make([]openAPIRouteKey, 0)
	for route := range left {
		if _, exists := right[route]; !exists {
			difference = append(difference, route)
		}
	}
	sort.Slice(difference, func(i int, j int) bool {
		if difference[i].Path != difference[j].Path {
			return difference[i].Path < difference[j].Path
		}
		return difference[i].Method < difference[j].Method
	})
	return difference
}

func formatOpenAPIRoutes(routes []openAPIRouteKey) string {
	if len(routes) == 0 {
		return "  (none)"
	}

	lines := make([]string, 0, len(routes))
	for _, route := range routes {
		lines = append(lines, fmt.Sprintf("  %s %s", route.Method, route.Path))
	}
	return strings.Join(lines, "\n")
}

func assertOpenAPICompleteness(t *testing.T, path string) {
	t.Helper()

	document, raw := loadOpenAPIDocument(t, path)
	if bytes.Contains(raw, []byte("#/components/schemas/SuccessEnvelope")) {
		t.Errorf("OpenAPI spec %s still references the untyped SuccessEnvelope", path)
	}
	assertOpenAPIRefsResolve(t, path, document)

	paths := mustJSONObject(t, document["paths"], "paths")
	for pathName, pathValue := range paths {
		pathItem := mustJSONObject(t, pathValue, "paths."+pathName)
		for method, operationValue := range pathItem {
			if !isOpenAPIOperationMethod(strings.ToUpper(method)) {
				continue
			}
			operation := mustJSONObject(t, operationValue, method+" "+pathName)
			operationID, _ := operation["operationId"].(string)
			location := strings.ToUpper(method) + " " + pathName + " (" + operationID + ")"

			if !containsRef(operation["parameters"], "#/components/parameters/RequestIDHeader") {
				t.Errorf("%s does not declare the X-Request-ID request header", location)
			}

			responses := mustJSONObject(t, operation["responses"], location+" responses")
			for status, responseValue := range responses {
				response := mustJSONObject(t, responseValue, location+" response "+status)
				headers, _ := response["headers"].(map[string]any)
				if headers == nil || headers["X-Request-ID"] == nil {
					t.Errorf("%s response %s does not declare X-Request-ID", location, status)
				}
			}

			if operationID != "getLiveness" {
				for _, status := range []string{"429", "503"} {
					if responses[status] == nil {
						t.Errorf("%s does not declare response %s", location, status)
					}
				}
			}

			route := openAPIRouteKey{Method: strings.ToUpper(method), Path: pathName}
			if !isPublicOpenAPIRoute(route) {
				for _, status := range []string{"401", "500"} {
					if responses[status] == nil {
						t.Errorf("authenticated %s does not declare response %s", location, status)
					}
				}
			}
		}
	}

	for _, operationID := range []string{"uploadObject", "uploadObjectBatch", "publishSiteUpload", "publishSiteFile"} {
		operation := findOpenAPIOperation(t, document, operationID)
		responses := mustJSONObject(t, operation["responses"], operationID+" responses")
		if responses["413"] == nil {
			t.Errorf("upload operation %s does not declare response 413", operationID)
		}
	}

	expectedSuccessSchemas := map[string]string{
		"getLiveness":                "LivenessEnvelope",
		"getReadiness":               "ReadinessEnvelope",
		"getSystemMetrics":           "SystemMetricsEnvelope",
		"getSystemStats":             "SystemStatsEnvelope",
		"updateSystemStorageQuota":   "StorageQuotaEnvelope",
		"deleteExplorerEntriesBatch": "ExplorerBatchDeleteEnvelope",
		"listRecycleBinObjects":      "RecycleBinListEnvelope",
		"restoreRecycleBinObjects":   "RecycleBinRestoreEnvelope",
		"deleteRecycleBinObjects":    "RecycleBinDeleteEnvelope",
	}
	for operationID, schemaName := range expectedSuccessSchemas {
		operation := findOpenAPIOperation(t, document, operationID)
		responses := mustJSONObject(t, operation["responses"], operationID+" responses")
		if !containsRef(responses, "#/components/schemas/"+schemaName) {
			t.Errorf("operation %s does not use concrete response schema %s", operationID, schemaName)
		}
	}

	assertMultipartJSONEncoding(t, document, "uploadObjectBatch", "manifest")
	assertMultipartJSONEncoding(t, document, "publishSiteUpload", "manifest", "domains")
	assertMultipartJSONEncoding(t, document, "publishSiteFile", "domains")
	assertOpenAPIWireRepresentations(t, document)

	hostRouting := mustJSONObject(t, document["x-light-oss-host-routing"], "x-light-oss-host-routing")
	if !reflect.DeepEqual(hostRouting["methods"], []any{"GET", "HEAD"}) {
		t.Errorf("OpenAPI spec %s host routing methods = %#v, want GET and HEAD", path, hostRouting["methods"])
	}
	if !reflect.DeepEqual(hostRouting["response_statuses"], []any{float64(200), float64(404), float64(429), float64(500), float64(503)}) {
		t.Errorf("OpenAPI spec %s host routing statuses = %#v", path, hostRouting["response_statuses"])
	}
}

func assertOpenAPIWireRepresentations(t *testing.T, document map[string]any) {
	t.Helper()

	upload := findOpenAPIOperation(t, document, "uploadObject")
	requestBody := mustJSONObject(t, upload["requestBody"], "uploadObject requestBody")
	content := mustJSONObject(t, requestBody["content"], "uploadObject request content")
	if _, ok := content["*/*"]; !ok || len(content) != 1 {
		t.Errorf("uploadObject request media types = %#v, want only */*", reflect.ValueOf(content).MapKeys())
	}

	archive := findOpenAPIOperation(t, document, "downloadFolderArchive")
	archiveResponses := mustJSONObject(t, archive["responses"], "downloadFolderArchive responses")
	archiveSuccess := mustJSONObject(t, archiveResponses["200"], "downloadFolderArchive response 200")
	archiveHeaders := mustJSONObject(t, archiveSuccess["headers"], "downloadFolderArchive response 200 headers")
	if archiveHeaders["Content-Disposition"] == nil {
		t.Error("downloadFolderArchive response 200 does not declare Content-Disposition")
	}

	for _, operationID := range []string{"headObject", "headSiteRoot", "headSitePath"} {
		operation := findOpenAPIOperation(t, document, operationID)
		responses := mustJSONObject(t, operation["responses"], operationID+" responses")
		for status, rawResponse := range responses {
			response := mustJSONObject(t, rawResponse, operationID+" response "+status)
			if response["content"] != nil {
				t.Errorf("%s response %s declares a body for HEAD", operationID, status)
			}
		}
	}

	for _, operationID := range []string{"downloadSiteRoot", "downloadSitePath"} {
		operation := findOpenAPIOperation(t, document, operationID)
		responses := mustJSONObject(t, operation["responses"], operationID+" responses")
		internalError := mustJSONObject(t, responses["500"], operationID+" response 500")
		if internalError["content"] != nil {
			t.Errorf("%s response 500 declares a body for the status-only website error", operationID)
		}
		unavailable := mustJSONObject(t, responses["503"], operationID+" response 503")
		unavailableContent := mustJSONObject(t, unavailable["content"], operationID+" response 503 content")
		if unavailableContent["application/json"] == nil {
			t.Errorf("%s response 503 does not declare the rate-limit JSON error variant", operationID)
		}
	}
}

func assertMultipartJSONEncoding(t *testing.T, document map[string]any, operationID string, fields ...string) {
	t.Helper()
	operation := findOpenAPIOperation(t, document, operationID)
	requestBody := mustJSONObject(t, operation["requestBody"], operationID+" requestBody")
	content := mustJSONObject(t, requestBody["content"], operationID+" request content")
	multipart := mustJSONObject(t, content["multipart/form-data"], operationID+" multipart/form-data")
	encoding := mustJSONObject(t, multipart["encoding"], operationID+" multipart encoding")
	for _, field := range fields {
		fieldEncoding := mustJSONObject(t, encoding[field], operationID+" encoding "+field)
		if fieldEncoding["contentType"] != "application/json" {
			t.Errorf("operation %s multipart field %s contentType = %#v, want application/json", operationID, field, fieldEncoding["contentType"])
		}
	}
}

func assertOpenAPIRefsResolve(t *testing.T, path string, document map[string]any) {
	t.Helper()
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if ref, ok := typed["$ref"].(string); ok {
				if !strings.HasPrefix(ref, "#/") {
					t.Errorf("OpenAPI spec %s contains unsupported external ref %q", path, ref)
				} else if _, ok := resolveJSONPointer(document, ref); !ok {
					t.Errorf("OpenAPI spec %s contains unresolved ref %q", path, ref)
				}
			}
			for _, child := range typed {
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(document)
}

func resolveJSONPointer(document map[string]any, ref string) (any, bool) {
	var current any = document
	for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[token]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func containsRef(value any, expected string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if typed["$ref"] == expected {
			return true
		}
		for _, child := range typed {
			if containsRef(child, expected) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsRef(child, expected) {
				return true
			}
		}
	}
	return false
}

func findOpenAPIOperation(t *testing.T, document map[string]any, operationID string) map[string]any {
	t.Helper()
	paths := mustJSONObject(t, document["paths"], "paths")
	for pathName, pathValue := range paths {
		pathItem := mustJSONObject(t, pathValue, "paths."+pathName)
		for method, operationValue := range pathItem {
			if !isOpenAPIOperationMethod(strings.ToUpper(method)) {
				continue
			}
			operation := mustJSONObject(t, operationValue, method+" "+pathName)
			if operation["operationId"] == operationID {
				return operation
			}
		}
	}
	t.Fatalf("OpenAPI operationId %q was not found", operationID)
	return nil
}

func mustJSONObject(t *testing.T, value any, location string) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI value %s is %T, want object", location, value)
	}
	return object
}

func loadOpenAPIDocument(t *testing.T, path string) (map[string]any, []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenAPI spec %s: %v", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI spec %s: %v", path, err)
	}
	return document, raw
}

func assertOpenAPISpecsStructurallyEquivalent(t *testing.T, englishPath string, chinesePath string) {
	t.Helper()
	english, _ := loadOpenAPIDocument(t, englishPath)
	chinese, _ := loadOpenAPIDocument(t, chinesePath)

	normalizeOpenAPILocalization(english)
	normalizeOpenAPILocalization(chinese)
	if !reflect.DeepEqual(english, chinese) {
		englishJSON, _ := json.MarshalIndent(english, "", "  ")
		chineseJSON, _ := json.MarshalIndent(chinese, "", "  ")
		t.Fatalf(
			"English and Chinese OpenAPI specs differ outside localized text\nEnglish normalized:\n%s\nChinese normalized:\n%s",
			englishJSON,
			chineseJSON,
		)
	}
}

func normalizeOpenAPILocalization(value any) {
	localizedKeys := map[string]struct{}{
		"description": {},
		"example":     {},
		"examples":    {},
		"summary":     {},
		"tags":        {},
		"title":       {},
	}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if _, localized := localizedKeys[key]; localized {
					delete(typed, key)
					continue
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
}
