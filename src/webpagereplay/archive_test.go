// Copyright 2017 The Chromium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package webpagereplay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func createArchivedRequest(t *testing.T, ustr string, header http.Header) *ArchivedRequest {
	req := httptest.NewRequest("GET", ustr, nil)
	req.Header = header
	resp := http.Response{StatusCode: 200}
	archivedRequest, err := serializeRequest(req, &resp)
	if err != nil {
		t.Fatalf("failed serialize request %s: %v", ustr, err)
		return nil
	}
	return archivedRequest
}

// newRequest returns a request with the given method, URL and body.
func newRequest(method, ustr, body string) *http.Request {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return httptest.NewRequest(method, ustr, r)
}

// newResponse returns a 200 response with the given body.
func newResponse(body string) *http.Response {
	return &http.Response{
		StatusCode:    200,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// createArchivedRequestFor returns an archived request for req, whose response
// body is respBody.
func createArchivedRequestFor(t *testing.T, req *http.Request, respBody string) *ArchivedRequest {
	t.Helper()
	archivedRequest, err := serializeRequest(req, newResponse(respBody))
	if err != nil {
		t.Fatalf("failed to serialize request %s %s: %v", req.Method, req.URL, err)
	}
	return archivedRequest
}

// findResponseBody returns the body of the archived response that FindRequest
// returns for req.
func findResponseBody(t *testing.T, a *Archive, req *http.Request) string {
	t.Helper()
	_, resp, err := a.FindRequest(req)
	if err != nil {
		t.Fatalf("failed to find %s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read the response for %s %s: %v", req.Method, req.URL, err)
	}
	return string(body)
}

func validateTrim(t *testing.T, f func(req *http.Request, resp *http.Response) (bool, error), a Archive, expected int) {
	b, err := a.Trim(f)
	if err != nil {
		t.Fatalf("Trim returned an error: %v", err)
	}
	actual := len(b.Requests)
	if actual != expected {
		t.Fatalf("Expected %d request in archive b, found %d.", expected, actual)
	}
}

func TestFindRequestFuzzyMatching(t *testing.T) {
	a := newArchive()
	const u = "https://example.com/a/b/c/+/query?usegapi=1&foo=bar&c=d"
	const host = "example.com"
	req := createArchivedRequest(t, u, nil)
	a.Requests[host] = make(map[string][]*ArchivedRequest)
	a.Requests[host][u] = []*ArchivedRequest{req}
	prepareArchiveForReplay(&a)

	const newUrl = "https://example.com/a/b/c/+/query?usegapi=1&foo=yay&c=d&a=y"
	newReq := httptest.NewRequest("GET", newUrl, nil)

	_, foundResp, err := a.FindRequest(newReq)
	if err != nil {
		t.Fatalf("failed to find %s: %v", newUrl, err)
	}
	if got, want := foundResp.StatusCode, 200; got != want {
		t.Fatalf("status codes do not match. Got: %d. Want: %d", got, want)
	}
}

// Regression test for updating bestRatio when matching query params.
func TestFindClosest(t *testing.T) {
	a := newArchive()
	const host = "example.com"
	a.Requests[host] = make(map[string][]*ArchivedRequest)
	// Store three requests. u1 and u2 match equally well. u1 is chosen because
	// u1<u2.
	const u1 = "https://example.com/index.html?a=f&c=e"
	a.Requests[host][u1] = []*ArchivedRequest{createArchivedRequest(t, u1, nil)}

	const u2 = "https://example.com/index.html?a=g&c=e"
	a.Requests[host][u2] = []*ArchivedRequest{createArchivedRequest(t, u2, nil)}

	const u3 = "https://example.com/index.html?a=b&c=d"
	a.Requests[host][u3] = []*ArchivedRequest{createArchivedRequest(t, u3, nil)}

	prepareArchiveForReplay(&a)

	const newUrl = "https://example.com/index.html?c=e"
	newReq := httptest.NewRequest("GET", newUrl, nil)

	// Check that u1 is returned. FindRequest was previously non-deterministic,
	// due to random map iteration, so run the test several times.
	for i := 0; i < 10; i++ {
		foundReq, foundResp, err := a.FindRequest(newReq)
		if err != nil {
			t.Fatalf("failed to find %s: %v", newUrl, err)
		}
		if got, want := foundResp.StatusCode, 200; got != want {
			t.Fatalf("status codes do not match. Got: %d. Want: %d", got, want)
		}

		query := foundReq.URL.Query()
		if query.Get("a") != "f" || query.Get("c") != "e" {
			t.Fatalf("wrong request is matched\nexpected: %s\nactual: %s", u1, foundReq.URL)
		}
	}
}

// Regression test for https://github.com/catapult-project/catapult/issues/3727
func TestMatchHeaders(t *testing.T) {
	a := newArchive()
	const u = "https://example.com/mail/"
	const host = "example.com"
	headers := http.Header{}
	headers.Set("Accept", "text/html")
	headers.Set("Accept-Language", "en-Us,en;q=0.8")
	headers.Set("Accept-Encoding", "gzip, deflate, br")
	headers.Set("Cookie", "FOO=FOO")
	{
		req := createArchivedRequest(t, u, headers)
		a.Requests[host] = make(map[string][]*ArchivedRequest)
		a.Requests[host][u] = []*ArchivedRequest{req}
	}
	{
		headers.Set("Cookie", "FOO=BAR;SSID=XXhdfdf;LOGIN=HELLO")
		req := createArchivedRequest(t, u, headers)
		a.Requests[host][u] = append(a.Requests[host][u], req)
	}
	prepareArchiveForReplay(&a)

	newReq := httptest.NewRequest("GET", u, nil)
	newReq.Header = headers

	foundReq, _, err := a.FindRequest(newReq)
	if err != nil {
		t.Fatalf("failed to find %s: %v", u, err)
	}
	if got, want := foundReq.Header.Get("Cookie"), headers.Get("Cookie"); got != want {
		t.Fatalf("expected %s , actual %s\n", want, got)
	}
}

// When no header matches, the archived request with the same url should still be returned.
func TestNoHeadersMatch(t *testing.T) {
	a := newArchive()
	const u = "https://example.com/mail/"
	const host = "example.com"
	headers := http.Header{}
	headers.Set("Accept-Encoding", "gzip, deflate, br")
	req := createArchivedRequest(t, u, headers)
	a.Requests[host] = make(map[string][]*ArchivedRequest)
	a.Requests[host][u] = []*ArchivedRequest{req}
	prepareArchiveForReplay(&a)

	newReq := httptest.NewRequest("GET", u, nil)
	newReq.Header = headers
	newReq.Header.Set("Accept-Encoding", "gzip, deflate")

	foundReq, _, err := a.FindRequest(newReq)
	if err != nil {
		t.Fatalf("failed to find %s: %v", u, err)
	}
	if got, want := foundReq.Header.Get("Accept-Encoding"), "gzip, deflate, br"; got != want {
		t.Fatalf("expected %s , actual %s\n", want, got)
	}
}

// Test tie-breaking when an archive contains multiple responses for the same request.
func TestTieBreak(t *testing.T) {
	a := newArchive()
	const u = "https://example.com/mail/"
	const host = "example.com"
	const header1 = "1"
	const header2 = "2"
	{
		headers := http.Header{}
		headers.Set("matched", header1)
		req := createArchivedRequest(t, u, headers)
		a.Requests[host] = make(map[string][]*ArchivedRequest)
		a.Requests[host][u] = []*ArchivedRequest{req}
	}
	{
		headers := http.Header{}
		headers.Set("matched", header2)
		req := createArchivedRequest(t, u, headers)
		a.Requests[host][u] = append(a.Requests[host][u], req)
	}
	prepareArchiveForReplay(&a)

	newReq := httptest.NewRequest("GET", u, nil)
	newReq.Header = http.Header{}

	{
		foundReq, _, err := a.FindRequest(newReq)
		if err != nil {
			t.Fatalf("failed to find %s: %v", u, err)
		}
		if got, want := foundReq.Header.Get("matched"), header1; got != want {
			t.Fatalf("expected %s , actual %s\n", want, got)
		}
	}

	{
		foundReq, _, err := a.FindRequest(newReq)
		if err != nil {
			t.Fatalf("failed to find %s: %v", u, err)
		}
		if got, want := foundReq.Header.Get("matched"), header1; got != want {
			t.Fatalf("expected %s , actual %s\n", want, got)
		}
	}
}

// Test tie breaking in chronological order when an archive contains multiple responses for
// the same request.
func TestTieBreakChronologicalOrder(t *testing.T) {
	a := newArchive()
	const u = "https://example.com/mail/"
	const host = "example.com"
	const header1 = "1"
	const header2 = "2"
	{
		headers := http.Header{}
		headers.Set("matched", header1)
		req := createArchivedRequest(t, u, headers)
		a.Requests[host] = make(map[string][]*ArchivedRequest)
		a.Requests[host][u] = []*ArchivedRequest{req}
	}
	{
		headers := http.Header{}
		headers.Set("matched", header2)
		req := createArchivedRequest(t, u, headers)
		a.Requests[host][u] = append(a.Requests[host][u], req)
	}
	a.ServeResponseInChronologicalSequence = true
	prepareArchiveForReplay(&a)

	newReq := httptest.NewRequest("GET", u, nil)
	newReq.Header = http.Header{}

	{
		foundReq, _, err := a.FindRequest(newReq)
		if err != nil {
			t.Fatalf("failed to find %s: %v", u, err)
		}
		if got, want := foundReq.Header.Get("matched"), header1; got != want {
			t.Fatalf("expected %s , actual %s\n", want, got)
		}
	}

	{
		foundReq, _, err := a.FindRequest(newReq)
		if err != nil {
			t.Fatalf("failed to find %s: %v", u, err)
		}
		if got, want := foundReq.Header.Get("matched"), header2; got != want {
			t.Fatalf("expected %s , actual %s\n", want, got)
		}
	}

	// Test starting a new replay session to reset the serving sequence.
	{
		a.StartNewReplaySession()
		foundReq, _, err := a.FindRequest(newReq)
		if err != nil {
			t.Fatalf("failed to find %s: %v", u, err)
		}
		if got, want := foundReq.Header.Get("matched"), header1; got != want {
			t.Fatalf("expected %s , actual %s\n", want, got)
		}
	}
}

// A request whose exact URL was only recorded with another method matches a
// request recorded with its own method on the same path.
func TestFindRequestPrefersMethodOverExactURL(t *testing.T) {
	const u1 = "https://example.com/collect?t=1"
	const u2 = "https://example.com/collect?t=2"
	a := newArchive()
	a.Requests["example.com"] = map[string][]*ArchivedRequest{
		u1: {createArchivedRequestFor(t, newRequest("GET", u1, ""), "get")},
		u2: {createArchivedRequestFor(t, newRequest("POST", u2, "data"), "post")},
	}
	prepareArchiveForReplay(&a)

	if got, want := findResponseBody(t, &a, newRequest("POST", u1, "data")), "post"; got != want {
		t.Errorf("POST %s: got %q, want %q", u1, got, want)
	}
	if got, want := findResponseBody(t, &a, newRequest("GET", u1, "")), "get"; got != want {
		t.Errorf("GET %s: got %q, want %q", u1, got, want)
	}
}

// A request whose exact URL has several entries, none of them recorded with its
// method, matches a request recorded with its method on the same path instead
// of failing.
func TestFindRequestMethodMismatchWithSeveralEntries(t *testing.T) {
	const u1 = "https://example.com/cart.js?_=1"
	const u2 = "https://example.com/cart.js?_=2"
	a := newArchive()
	a.Requests["example.com"] = map[string][]*ArchivedRequest{
		u1: {
			createArchivedRequestFor(t, newRequest("GET", u1, ""), "get1"),
			createArchivedRequestFor(t, newRequest("GET", u1, ""), "get2"),
		},
		u2: {createArchivedRequestFor(t, newRequest("POST", u2, "data"), "post")},
	}
	prepareArchiveForReplay(&a)

	if got, want := findResponseBody(t, &a, newRequest("POST", u1, "data")), "post"; got != want {
		t.Errorf("POST %s: got %q, want %q", u1, got, want)
	}
}

// Fuzzy URL matching only considers URLs recorded with the request's method,
// even if the query of a URL recorded with another method matches better.
func TestFindRequestFuzzyMatchingRequiresMethod(t *testing.T) {
	const getURL = "https://example.com/api?op=read&id=1"
	const postURL = "https://example.com/api?op=write&id=2"
	a := newArchive()
	a.Requests["example.com"] = map[string][]*ArchivedRequest{
		getURL:  {createArchivedRequestFor(t, newRequest("GET", getURL, ""), "get")},
		postURL: {createArchivedRequestFor(t, newRequest("POST", postURL, "data"), "post")},
	}
	prepareArchiveForReplay(&a)

	const u1 = "https://example.com/api?op=read&id=3"
	if got, want := findResponseBody(t, &a, newRequest("POST", u1, "data")), "post"; got != want {
		t.Errorf("POST %s: got %q, want %q", u1, got, want)
	}
	const u2 = "https://example.com/api?op=write&id=3"
	if got, want := findResponseBody(t, &a, newRequest("GET", u2, "")), "get"; got != want {
		t.Errorf("GET %s: got %q, want %q", u2, got, want)
	}
}

// Only requests recorded with the request's method match. For instance, a
// recorded CORS preflight is not served for a GET or a POST.
func TestFindRequestRequiresMethod(t *testing.T) {
	const u = "https://example.com/api?a=1"
	a := newArchive()
	a.Requests["example.com"] = map[string][]*ArchivedRequest{
		u: {createArchivedRequestFor(t, newRequest("OPTIONS", u, ""), "preflight")},
	}
	prepareArchiveForReplay(&a)

	for _, tc := range []struct{ method, ustr string }{
		{"GET", u},
		{"POST", u},
		{"POST", "https://example.com/api?a=2"},
	} {
		if _, _, err := a.FindRequest(newRequest(tc.method, tc.ustr, "")); err != ErrNotFound {
			t.Errorf("%s %s: got error %v, want %v", tc.method, tc.ustr, err, ErrNotFound)
		}
	}
	if got, want := findResponseBody(t, &a, newRequest("OPTIONS", u, "")), "preflight"; got != want {
		t.Errorf("OPTIONS %s: got %q, want %q", u, got, want)
	}
}

// A single archived request doesn't match a request with another method. The
// method check used to be skipped when there was only one archived request.
func TestFindBestMatchInArchivedRequestSetRequiresMethod(t *testing.T) {
	const u = "https://example.com/api"
	a := newArchive()
	prepareArchiveForReplay(&a)

	reqs := []*ArchivedRequest{createArchivedRequestFor(t, newRequest("OPTIONS", u, ""), "preflight")}
	if _, _, err := a.findBestMatchInArchivedRequestSet(newRequest("POST", u, ""), reqs); err != ErrNotFound {
		t.Errorf("POST %s: got error %v, want %v", u, err, ErrNotFound)
	}
}

// A single archived request is served again and again in the chronological
// sequence, until a new replay session starts.
func TestFindRequestSingleRequestInChronologicalSequence(t *testing.T) {
	const u = "https://example.com/data.json"
	a := newArchive()
	a.Requests["example.com"] = map[string][]*ArchivedRequest{
		u: {createArchivedRequestFor(t, newRequest("GET", u, ""), "only")},
	}
	a.ServeResponseInChronologicalSequence = true
	prepareArchiveForReplay(&a)

	for i := 1; i <= 3; i++ {
		if got, want := findResponseBody(t, &a, newRequest("GET", u, "")), "only"; got != want {
			t.Errorf("GET %s (#%d): got %q, want %q", u, i, got, want)
		}
	}
	a.StartNewReplaySession()
	if got, want := findResponseBody(t, &a, newRequest("GET", u, "")), "only"; got != want {
		t.Errorf("GET %s in a new replay session: got %q, want %q", u, got, want)
	}
}

// Only the chronological sequence keeps track of which requests were served.
// Outside of it, finding a request doesn't write to the archive.
func TestFindRequestMarksServedRequestsOnlyInChronologicalSequence(t *testing.T) {
	const u = "https://example.com/data.json"
	for _, chronological := range []bool{false, true} {
		a := newArchive()
		a.Requests["example.com"] = map[string][]*ArchivedRequest{
			u: {
				createArchivedRequestFor(t, newRequest("GET", u, ""), "first"),
				createArchivedRequestFor(t, newRequest("GET", u, ""), "second"),
			},
		}
		a.ServeResponseInChronologicalSequence = chronological
		prepareArchiveForReplay(&a)

		for i := 0; i < 3; i++ {
			findResponseBody(t, &a, newRequest("GET", u, ""))
		}
		marked := 0
		for _, r := range a.Requests["example.com"][u] {
			if r.LastServedSessionId == a.CurrentSessionId {
				marked++
			}
		}
		// The first two lookups serve both requests in the sequence.
		want := 0
		if chronological {
			want = 2
		}
		if marked != want {
			t.Errorf("chronological sequence %v: %d requests marked as served, want %d", chronological, marked, want)
		}
	}
}

// With fuzzy URL matching disabled, only the exact URL can match, and only with
// the request's method.
func TestFindRequestMethodWithFuzzyURLMatchingDisabled(t *testing.T) {
	const u1 = "https://example.com/collect?t=1"
	const u2 = "https://example.com/collect?t=2"
	a := newArchive()
	a.Requests["example.com"] = map[string][]*ArchivedRequest{
		u1: {createArchivedRequestFor(t, newRequest("GET", u1, ""), "get")},
		u2: {createArchivedRequestFor(t, newRequest("POST", u2, "data"), "post")},
	}
	a.DisableFuzzyURLMatching = true
	prepareArchiveForReplay(&a)

	if got, want := findResponseBody(t, &a, newRequest("GET", u1, "")), "get"; got != want {
		t.Errorf("GET %s: got %q, want %q", u1, got, want)
	}
	for _, ustr := range []string{u1, "https://example.com/collect?t=3"} {
		if _, _, err := a.FindRequest(newRequest("POST", ustr, "data")); err != ErrNotFound {
			t.Errorf("POST %s: got error %v, want %v", ustr, err, ErrNotFound)
		}
	}
}

// closestURLs only considers the URLs on the same path that were recorded with
// the method, and returns the ones whose queries match best, sorted.
func TestClosestURLs(t *testing.T) {
	const (
		read1  = "https://example.com/api?op=read&id=1"
		read2  = "https://example.com/api?op=read&id=2"
		write1 = "https://example.com/api?op=write&id=1"
		other  = "https://example.com/other?op=read&id=9"
	)
	hostMap := map[string][]*ArchivedRequest{
		read1:  {createArchivedRequestFor(t, newRequest("GET", read1, ""), "get")},
		read2:  {createArchivedRequestFor(t, newRequest("GET", read2, ""), "get")},
		write1: {createArchivedRequestFor(t, newRequest("POST", write1, "data"), "post")},
		other:  {createArchivedRequestFor(t, newRequest("POST", other, "data"), "post")},
	}
	u, err := url.Parse("https://example.com/api?op=read&id=9")
	if err != nil {
		t.Fatalf("failed to parse the URL: %v", err)
	}

	// read1 and read2 each have one query parameter in common with u, and
	// write1 has none.
	for _, tc := range []struct {
		method string
		want   []string
	}{
		{"GET", []string{read1, read2}},
		{"POST", []string{write1}},
		{"PUT", nil},
	} {
		if got := closestURLs(hostMap, u, tc.method); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.method, got, tc.want)
		}
	}
}

func TestMerge(t *testing.T) {
	a := newArchive()
	b := newArchive()
	const host = "example.com"
	a.Requests[host] = make(map[string][]*ArchivedRequest)
	b.Requests[host] = make(map[string][]*ArchivedRequest)
	// Create three requests with very closely matching params; only the first one
	// is different.
	const u1 = "https://example.com/index.html?a=AB&b=1&c=2"
	a.Requests[host][u1] = []*ArchivedRequest{createArchivedRequest(t, u1, nil)}
	b.Requests[host][u1] = []*ArchivedRequest{createArchivedRequest(t, u1, nil)}

	const u2 = "https://example.com/index.html?a=A&b=1&c=2"
	a.Requests[host][u2] = []*ArchivedRequest{createArchivedRequest(t, u2, nil)}

	const u3 = "https://example.com/index.html?a=B&b=1&c=2"
	b.Requests[host][u3] = []*ArchivedRequest{createArchivedRequest(t, u3, nil)}

	// Merging an archive with itself should yield the same results.
	if len(a.Requests[host]) != 2 {
		t.Fatalf("Expected 2 requests in archive a")
	}
	_ = a.Merge(&a, false)
	if len(a.Requests[host]) != 2 {
		t.Fatalf("Expected 2 requests in archive a")
	}

	if len(b.Requests[host]) != 2 {
		t.Fatalf("Expected 2 requests in archive b")
	}
	_ = b.Merge(&b, false)
	if len(b.Requests[host]) != 2 {
		t.Fatalf("Expected 2 requests in archive b")
	}

	// Merge b into a.
	_ = a.Merge(&b, false)
	if size := len(a.Requests[host]); size != 3 {
		t.Fatalf("Expected 3 requests in archive a but got %d", size)
	}
	if len(b.Requests[host]) != 2 {
		t.Fatalf("Expected 2 requests in archive b")
	}
}

// Test that requests with the same URL but different headers are not lost
// during merge.
func TestMergeDifferentHeaders(t *testing.T) {
	a := newArchive()
	b := newArchive()
	const host = "example.com"
	a.Requests[host] = make(map[string][]*ArchivedRequest)
	b.Requests[host] = make(map[string][]*ArchivedRequest)
	// Create two requests with the same URL but different headers
	const url = "https://example.com/index.html"

	h1 := http.Header{}
	h1.Set("User-Agent", "mobile")
	a.Requests[host][url] = []*ArchivedRequest{createArchivedRequest(t, url, h1)}

	h2 := http.Header{}
	h2.Set("User-Agent", "desktop")
	b.Requests[host][url] = []*ArchivedRequest{createArchivedRequest(t, url, h2)}

	// The merged archive should contain both requests.
	_ = a.Merge(&b, false)
	if len(a.Requests[host][url]) != 2 {
		t.Fatalf("Expected 2 requests in archive a, found %d",
			len(a.Requests[host][url]))
	}

	_ = b.Merge(&a, false)
	if len(b.Requests[host][url]) != 2 {
		t.Fatalf("Expected 2 requests in archive b, found %d",
			len(b.Requests[host][url]))
	}
}

func TestMergeWithDuplicates(t *testing.T) {
	a := newArchive()
	const host = "example.com"
	a.Requests[host] = make(map[string][]*ArchivedRequest)
	const url = "https://example.com/index.html"
	a.Requests[host][url] = []*ArchivedRequest{createArchivedRequest(t, url, nil)}

	if len(a.Requests[host][url]) != 1 {
		t.Fatalf("Expected 1 request in archive a")
	}
	_ = a.Merge(&a, true)
	if len(a.Requests[host][url]) != 2 {
		t.Fatalf("Expected 2 requests in archive a")
	}
}

func TestAdd(t *testing.T) {
	// generate a test server so we can capture and inspect the request
	testServer := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.Write([]byte("body"))
	}))
	defer func() { testServer.Close() }()

	a := newArchive()
	if len(a.Requests) != 0 {
		t.Fatalf("Expected empty archive")
	}

	requestURL, _ := url.Parse(testServer.URL)
	requestURLString := testServer.URL
	if err := a.Add("GET", requestURLString, AddModeAppend); err != nil {
		t.Fatalf("Error: %v", err)
	}

	requestMap := a.Requests[requestURL.Host]
	if len(requestMap) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}

	requestURLString2 := requestURLString + "?q=something"
	if err := a.Add("GET", requestURLString2, AddModeAppend); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if len(requestMap[requestURLString]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString2]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}

	// Append another request with the URL
	if err := a.Add("GET", requestURLString2, AddModeAppend); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if len(requestMap[requestURLString]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString2]) != 2 {
		t.Fatalf("Expected 1 requests in archive a")
	}

	// Add but ignore existing request.
	requestURLString3 := requestURLString + "/request3/?q=something_else"
	if err := a.Add("GET", requestURLString2, AddModeSkipExisting); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if len(requestMap[requestURLString]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString2]) != 2 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString3]) != 0 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	// Adding new requests works as usual.
	if err := a.Add("GET", requestURLString3, AddModeSkipExisting); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if len(requestMap[requestURLString]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString2]) != 2 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString3]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}

	// Add but overwrite existing ones
	requestURLString4 := requestURLString + "/request4/"
	if err := a.Add("GET", requestURLString2, AddModeOverwriteExisting); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if len(requestMap[requestURLString]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString2]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString3]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString4]) != 0 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	// Adding new requests works as usual.
	if err := a.Add("GET", requestURLString4, AddModeOverwriteExisting); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if len(requestMap[requestURLString]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString2]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString3]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}
	if len(requestMap[requestURLString4]) != 1 {
		t.Fatalf("Expected 1 requests in archive a")
	}

}

// With AddModeSkipExisting, a request is only skipped if a request with its
// method was recorded under its URL.
func TestAddArchivedRequestSkipExistingConsidersMethod(t *testing.T) {
	const u = "https://example.com/collect"
	a := newArchive()
	for _, tc := range []struct {
		method, respBody string
		wantRequests     int
	}{
		{"GET", "get1", 1},
		// The URL was only recorded with GET.
		{"POST", "post1", 2},
		{"POST", "post2", 2},
		{"GET", "get2", 2},
	} {
		if err := a.AddArchivedRequest(newRequest(tc.method, u, "data"), newResponse(tc.respBody), AddModeSkipExisting); err != nil {
			t.Fatalf("failed to add %s %s: %v", tc.method, u, err)
		}
		if got := len(a.Requests["example.com"][u]); got != tc.wantRequests {
			t.Fatalf("after adding %s %s: got %d requests, want %d", tc.method, u, got, tc.wantRequests)
		}
	}
	prepareArchiveForReplay(&a)
	for method, want := range map[string]string{"GET": "get1", "POST": "post1"} {
		if got := findResponseBody(t, &a, newRequest(method, u, "data")); got != want {
			t.Errorf("%s %s: got %q, want %q", method, u, got, want)
		}
	}
}

// Add does not consider a request recorded with another method a duplicate.
func TestAddSkipExistingConsidersMethod(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		io.WriteString(res, req.Method)
	}))
	defer testServer.Close()

	a := newArchive()
	u := testServer.URL + "/collect"
	requestURL, err := url.Parse(u)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", u, err)
	}
	for i, method := range []string{"GET", "POST", "POST", "GET"} {
		if err := a.Add(method, u, AddModeSkipExisting); err != nil {
			t.Fatalf("failed to add %s %s: %v", method, u, err)
		}
		// Only the first GET and the first POST are added.
		if got, want := len(a.Requests[requestURL.Host][u]), min(i+1, 2); got != want {
			t.Fatalf("after adding %s %s: got %d requests, want %d", method, u, got, want)
		}
	}
	prepareArchiveForReplay(&a)
	for _, method := range []string{"GET", "POST"} {
		if got := findResponseBody(t, &a, newRequest(method, u, "")); got != method {
			t.Errorf("%s %s: got %q, want %q", method, u, got, method)
		}
	}
}

func TestArchiveClone(t *testing.T) {
	val := 0.123
	original := Archive{
		Requests: map[string]map[string][]*ArchivedRequest{
			"host": {"url": {&ArchivedRequest{}}},
		},
		Certs: map[string][]byte{
			"host": {1, 2, 3},
		},
		NegotiatedProtocol: map[string]string{
			"host": "h2",
		},
		DeterministicTimeSeedMs:              12345,
		ConstantMathRandomResult:             &val,
		ServeResponseInChronologicalSequence: true,
		CurrentSessionId:                     10,
		DisableFuzzyURLMatching:              true,
		Metadata:                             "test metadata",
		InjectedScripts: map[string]string{
			"s1.js": "var x = 1;",
		},
	}

	clone := original.cloneFieldsExceptRequests()

	expectEqual := func(fieldName string) {
		t.Helper()
		origField := reflect.ValueOf(original).FieldByName(fieldName).Interface()
		cloneField := reflect.ValueOf(clone).FieldByName(fieldName).Interface()
		if !reflect.DeepEqual(origField, cloneField) {
			t.Errorf("%s mismatch: got %v, want %v", fieldName, cloneField, origField)
		}
	}

	// Note that cloneFieldsExceptRequests() ends with "except requests."
	// Ensure they are NOT cloned.
	if len(clone.Requests) != 0 {
		t.Errorf("Requests should be empty in clone, got %v", clone.Requests)
	}

	// Ensure all other fields are correctly cloned.
	expectEqual("Certs")
	expectEqual("NegotiatedProtocol")
	expectEqual("DeterministicTimeSeedMs")
	expectEqual("ConstantMathRandomResult")
	expectEqual("ServeResponseInChronologicalSequence")
	expectEqual("CurrentSessionId")
	expectEqual("DisableFuzzyURLMatching")
	expectEqual("Metadata")
	expectEqual("InjectedScripts")

	// Some extra tests for fields that can assume more than one class of value.
	original.ConstantMathRandomResult = nil
	clone2 := original.cloneFieldsExceptRequests()
	if clone2.ConstantMathRandomResult != nil {
		t.Errorf("ConstantMathRandomResult should be nil, got %v",
			clone2.ConstantMathRandomResult)
	}
}

func TestArchiveCloneIndependence(t *testing.T) {
	val := 0.123
	original := Archive{
		Requests: map[string]map[string][]*ArchivedRequest{
			"host": {"url": {&ArchivedRequest{}}},
		},
		Certs: map[string][]byte{
			"host": {1, 2, 3},
		},
		NegotiatedProtocol: map[string]string{
			"host": "h2",
		},
		DeterministicTimeSeedMs:              12345,
		ConstantMathRandomResult:             &val,
		ServeResponseInChronologicalSequence: true,
		CurrentSessionId:                     10,
		DisableFuzzyURLMatching:              true,
		Metadata:                             "test metadata",
		InjectedScripts: map[string]string{
			"s1.js": "var x = 1;",
		},
	}

	clone := original.cloneFieldsExceptRequests()

	expectUnequal := func(fieldName string) {
		t.Helper()
		origField := reflect.ValueOf(original).FieldByName(fieldName).Interface()
		cloneField := reflect.ValueOf(clone).FieldByName(fieldName).Interface()
		if reflect.DeepEqual(origField, cloneField) {
			t.Errorf("Modifying clone's %s modified original", fieldName)
		}
	}

	clone.Requests["new-host"] = map[string][]*ArchivedRequest{}
	expectUnequal("Requests")

	clone.Certs["host"][0] = 99
	expectUnequal("Certs")

	clone.NegotiatedProtocol["host"] = "http/1.1"
	expectUnequal("NegotiatedProtocol")

	clone.DeterministicTimeSeedMs = 54321
	expectUnequal("DeterministicTimeSeedMs")

	*clone.ConstantMathRandomResult = 0.456
	expectUnequal("ConstantMathRandomResult")

	clone.ServeResponseInChronologicalSequence = false
	expectUnequal("ServeResponseInChronologicalSequence")

	clone.CurrentSessionId = 20
	expectUnequal("CurrentSessionId")

	clone.DisableFuzzyURLMatching = false
	expectUnequal("DisableFuzzyURLMatching")

	clone.Metadata = "modified metadata"
	expectUnequal("Metadata")

	clone.InjectedScripts["s1.js"] = "modified"
	expectUnequal("InjectedScripts")
}

func TestArchiveMetadata(t *testing.T) {
	original := Archive{
		Metadata: "test metadata\nwith multiple lines",
		Requests: make(map[string]map[string][]*ArchivedRequest),
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var unmarshalled Archive
	if err := json.Unmarshal(data, &unmarshalled); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if original.Metadata != unmarshalled.Metadata {
		t.Errorf("Metadata mismatch: got %v, want %v",
			unmarshalled.Metadata, original.Metadata)
	}
}

func TestArchiveJSON_DeterministicTimeSeedMs(t *testing.T) {
	original := Archive{
		DeterministicTimeSeedMs: 67890,
		Requests:                make(map[string]map[string][]*ArchivedRequest),
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var unmarshalled Archive
	if err := json.Unmarshal(data, &unmarshalled); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if original.DeterministicTimeSeedMs != unmarshalled.DeterministicTimeSeedMs {
		t.Errorf("DeterministicTimeSeedMs mismatch: got %v, want %v",
			unmarshalled.DeterministicTimeSeedMs, original.DeterministicTimeSeedMs)
	}
}

func TestArchiveJSON_WithConstantMathRandomResult(t *testing.T) {
	val := 0.456
	original := Archive{
		ConstantMathRandomResult: &val,
		Requests:                 make(map[string]map[string][]*ArchivedRequest),
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var unmarshalled Archive
	if err := json.Unmarshal(data, &unmarshalled); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if unmarshalled.ConstantMathRandomResult == nil ||
		*original.ConstantMathRandomResult !=
			*unmarshalled.ConstantMathRandomResult {
		t.Errorf("ConstantMathRandomResult mismatch: got %v, want %v",
			unmarshalled.ConstantMathRandomResult, original.ConstantMathRandomResult)
	}
}

func TestArchiveJSON_WithoutConstantMathRandomResult(t *testing.T) {
	original := Archive{
		ConstantMathRandomResult: nil,
		Requests:                 make(map[string]map[string][]*ArchivedRequest),
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var unmarshalled Archive
	if err := json.Unmarshal(data, &unmarshalled); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if unmarshalled.ConstantMathRandomResult != nil {
		t.Errorf("ConstantMathRandomResult should be nil, got %v",
			unmarshalled.ConstantMathRandomResult)
	}
}

func TestArchiveJSON_InjectedScripts(t *testing.T) {
	original := newArchive()
	original.InjectedScripts = map[string]string{
		"s1.js": "var x = 1;",
		"s2.js": "var y = 2;",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var unmarshalled Archive
	if err := json.Unmarshal(data, &unmarshalled); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if !reflect.DeepEqual(unmarshalled.InjectedScripts, original.InjectedScripts) {
		t.Errorf("InjectedScripts mismatch in unmarshalled: got %v, want %v",
			unmarshalled.InjectedScripts, original.InjectedScripts)
	}
}

func TestTrim(t *testing.T) {
	a := newArchive()

	const host1 = "example.com"
	const host2 = "example.gov"
	const host3 = "example.org"
	const u1 = "https://example.com/index.html?a=A"
	const u2 = "https://example.gov/index.html?a=A"
	a.Requests[host1] = make(map[string][]*ArchivedRequest)
	a.Requests[host1][u1] = []*ArchivedRequest{createArchivedRequest(t, u1, nil)}
	a.Requests[host2] = make(map[string][]*ArchivedRequest)
	a.Requests[host2][u2] = []*ArchivedRequest{createArchivedRequest(t, u2, nil)}

	validateTrim(t, func(req *http.Request, resp *http.Response) (bool, error) { return true, nil }, a, 0)

	validateTrim(t, func(req *http.Request, resp *http.Response) (bool, error) { return false, nil }, a, 2)

	validateTrim(t, func(req *http.Request, resp *http.Response) (bool, error) { return req.Host == host1, nil }, a, 1)

	validateTrim(t, func(req *http.Request, resp *http.Response) (bool, error) { return req.Host == host2, nil }, a, 1)

	validateTrim(t, func(req *http.Request, resp *http.Response) (bool, error) { return req.Host == host3, nil }, a, 2)
}
