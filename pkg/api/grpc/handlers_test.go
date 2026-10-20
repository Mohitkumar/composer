package grpc

import (
	"context"
	"testing"

	workflowpb "github.com/mohitkumar/composer/pkg/api/proto/gen/go/workflow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Requests are validated before the coordinator is touched, so a service
// without one is enough here.
func TestReportTaskResultValidation(t *testing.T) {
	s := NewWorkflowService(nil)
	cases := map[string]*workflowpb.ReportTaskResultRequest{
		"missing ids":    {Status: "completed"},
		"missing run":    {TaskId: "t", NodeId: "n", Status: "completed"},
		"unknown status": {TaskId: "t", RunId: "r", NodeId: "n", Status: "done"},
	}
	for name, req := range cases {
		_, err := s.ReportTaskResult(context.Background(), req)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: got %v, want InvalidArgument", name, err)
		}
	}
}

func TestLeaseValidation(t *testing.T) {
	s := NewWorkflowService(nil)
	for _, req := range []*workflowpb.AcquireTaskLeaseRequest{
		{Owner: "w", RunId: "r"},
		{TaskId: "t", Owner: "w"},
		{TaskId: "t", RunId: "r"},
	} {
		if _, err := s.AcquireTaskLease(context.Background(), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("acquire %+v: %v", req, err)
		}
		if _, err := s.RenewTaskLease(context.Background(), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("renew %+v: %v", req, err)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if firstNonEmpty("", "b", "c") != "b" || firstNonEmpty("", "") != "" {
		t.Fatal("firstNonEmpty")
	}
}
