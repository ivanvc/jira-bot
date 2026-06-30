package http

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenderTransitionSuccessPage verifies the success page content when the
// live transition from setup to oauth2 mode succeeds.
// Validates: Requirements 6.1, 6.3
func TestRenderTransitionSuccessPage(t *testing.T) {
	rec := httptest.NewRecorder()
	cloudID := "test-cloud-id-123"

	renderTransitionSuccessPage(rec, cloudID)

	result := rec.Result()
	defer result.Body.Close()

	body := rec.Body.String()

	// Status should be 200 OK
	assert.Equal(t, 200, result.StatusCode, "success page should return HTTP 200")

	// Must contain "operational" (Requirement 6.1)
	assert.True(t, strings.Contains(strings.ToLower(body), "operational"),
		"success page should contain 'operational'")

	// Must contain "no pod restart" or "no restart" (Requirement 6.1)
	lowerBody := strings.ToLower(body)
	assert.True(t, strings.Contains(lowerBody, "no pod restart") || strings.Contains(lowerBody, "no restart"),
		"success page should contain 'no pod restart' or 'no restart'")

	// Must contain the cloud ID
	assert.Contains(t, body, cloudID, "success page should contain the cloud ID")
}

// TestRenderTransitionFailurePage verifies the failure page content when the
// live transition fails but tokens were persisted.
// Validates: Requirements 6.2, 6.3
func TestRenderTransitionFailurePage(t *testing.T) {
	rec := httptest.NewRecorder()
	cloudID := "test-cloud-id-456"

	renderTransitionFailurePage(rec, cloudID)

	result := rec.Result()
	defer result.Body.Close()

	body := rec.Body.String()

	// Status should be 200 OK
	assert.Equal(t, 200, result.StatusCode, "failure page should return HTTP 200")

	// Must contain "restart the pod" (Requirement 6.2)
	assert.True(t, strings.Contains(strings.ToLower(body), "restart the pod"),
		"failure page should contain 'restart the pod'")

	// Must contain the cloud ID
	assert.Contains(t, body, cloudID, "failure page should contain the cloud ID")
}

// TestTransitionPages_VisuallyDistinct verifies that the success and failure
// pages have different headings so the operator can distinguish them visually.
// Validates: Requirement 6.3
func TestTransitionPages_VisuallyDistinct(t *testing.T) {
	successRec := httptest.NewRecorder()
	failureRec := httptest.NewRecorder()
	cloudID := "test-cloud-id-789"

	renderTransitionSuccessPage(successRec, cloudID)
	renderTransitionFailurePage(failureRec, cloudID)

	successBody := successRec.Body.String()
	failureBody := failureRec.Body.String()

	// Extract <h1> content from both pages
	h1Re := regexp.MustCompile(`<h1[^>]*>(.*?)</h1>`)

	successH1 := h1Re.FindStringSubmatch(successBody)
	failureH1 := h1Re.FindStringSubmatch(failureBody)

	require.NotNil(t, successH1, "success page should contain an <h1> heading")
	require.NotNil(t, failureH1, "failure page should contain an <h1> heading")

	// Headings should be different (visually distinct)
	assert.NotEqual(t, successH1[1], failureH1[1],
		"success and failure pages should have different <h1> headings for visual distinction")

	// Both pages should have <h1> content (not empty)
	assert.NotEmpty(t, successH1[1], "success page <h1> should not be empty")
	assert.NotEmpty(t, failureH1[1], "failure page <h1> should not be empty")
}
