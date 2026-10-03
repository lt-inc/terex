package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"terex/main-be/internal/domain"
	"terex/main-be/internal/service/workflow"
	"testing"
	"time"
)

type conflictRepo struct{ domain.WorkflowRepo }

func (*conflictRepo) ActPublication(context.Context, int64, string, int, time.Time) error {
	return domain.ErrConflict
}

func TestWorkflowVersionConflictIsHTTP409(t *testing.T) {
	a := &API{Workflow: &workflow.Service{Repo: &conflictRepo{}}}
	r := httptest.NewRequest(http.MethodPost, "/api/publications/7/cancel", strings.NewReader(`{"expected_version":2}`))
	recorder := httptest.NewRecorder()
	a.Router().ServeHTTP(recorder, r)
	if recorder.Code != http.StatusConflict {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}

func TestWorkflowRejectsMalformedAndInvalidTargets(t *testing.T) {
	a := &API{Workflow: &workflow.Service{}}
	for _, body := range []string{`{`, `{"expected_version":2,"targets":[{"account_id":1},{"account_id":1}]}`} {
		recorder := httptest.NewRecorder()
		a.Router().ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/sub_jobs/sub/targets", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest {
			t.Fatal(recorder.Code, recorder.Body.String())
		}
	}
}
