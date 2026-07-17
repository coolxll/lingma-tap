package storage

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coolxll/lingma-tap/internal/proto"
)

func TestCaptureStoresBinaryAndMultipartArtifact(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "capture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="tower.jpg"`)
	h.Set("Content-Type", "image/jpeg")
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte{0xff, 0xd8, 0x00, 0x01, 0x7f, 0xfe}
	if _, err := part.Write(original); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	rec := &proto.Record{
		Ts: "2026-07-18T00:00:00Z", Session: "flow-1", Index: 0, Direction: "C2S",
		Method: "PUT", URL: "https://lingma-api.example/algo/api/v2/image/upload?request_id=req123",
		Path: "/algo/api/v2/image/upload", EndpointType: proto.EndpointImageUpload,
		ReqMime: mw.FormDataContentType(), ReqBodyBlob: body.Bytes(), ReqSize: int64(body.Len()),
		BodyPhase: "complete", BodyComplete: true, CapturedSize: int64(body.Len()),
	}
	if err := db.SaveRecord(rec); err != nil {
		t.Fatal(err)
	}
	if rec.ID == 0 || len(rec.ArtifactIDs) != 1 {
		t.Fatalf("record id/artifacts not assigned: id=%d artifacts=%v", rec.ID, rec.ArtifactIDs)
	}

	captured, mimeType, truncated, err := db.GetRecordBody(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(captured, body.Bytes()) || mimeType != rec.ReqMime || truncated {
		t.Fatalf("captured body mismatch: bytes=%v mime=%q truncated=%v", captured, mimeType, truncated)
	}

	artifacts, err := db.GetArtifacts(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Filename != "tower.jpg" || artifacts[0].MIME != "image/jpeg" {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
	artifactBody, artifactMime, err := db.GetArtifactBody(artifacts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifactBody, original) || artifactMime != "image/jpeg" {
		t.Fatalf("artifact mismatch: %v %q", artifactBody, artifactMime)
	}
	if !strings.Contains(strings.Join(rec.CorrelationKeys, "\n"), "request_id:req123") {
		t.Fatalf("request id correlation key missing: %v", rec.CorrelationKeys)
	}

	loaded, err := db.RecentRecords(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].ArtifactIDs) != 1 {
		t.Fatalf("summary did not retain artifact ids: %+v", loaded)
	}
	if _, err := io.ReadAll(bytes.NewReader(captured)); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureDoesNotCreateArtifactFromTruncatedMultipart(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "truncated.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rec := &proto.Record{
		Ts: "2026-07-18T00:00:00Z", Session: "flow-truncated", Index: 0, Direction: "C2S",
		Method: "PUT", URL: "https://lingma-api.example/algo/api/v2/image/upload",
		Path: "/algo/api/v2/image/upload", EndpointType: proto.EndpointImageUpload,
		ReqMime: "multipart/form-data; boundary=missing", ReqBodyBlob: []byte("partial-file-data"),
		ReqSize: 100, BodyPhase: "error", BodyComplete: false, BodyTruncated: true,
	}
	if err := db.SaveRecord(rec); err != nil {
		t.Fatal(err)
	}
	artifacts, err := db.GetArtifacts(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("truncated multipart created artifacts: %+v", artifacts)
	}
}
