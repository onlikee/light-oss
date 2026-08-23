package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
		})
	}
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
		{Method: http.MethodGet, Path: "/healthz"}:                                {},
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
