package webhook

import (
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/pkg/errors"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/klog"
)

var errEmptyBody = errors.New("empty body")

func readBody(r *http.Request, n int64) ([]byte, error) {
	if r.Body == nil {
		return nil, errEmptyBody
	}
	lr := &io.LimitedReader{R: r.Body, N: n}
	b, err := ioutil.ReadAll(lr)
	if err != nil {
		return b, err
	}
	if lr.N <= 0 {
		return b, errors.Errorf("expected at most %d bytes, got more", n)
	}
	if len(b) == 0 {
		return nil, errEmptyBody
	}
	return b, nil
}

func decodeAdmissionReview(decoder runtime.Decoder, body []byte) (*admissionv1beta1.AdmissionReview, error) {
	ar := admissionv1beta1.AdmissionReview{}
	_, _, err := decoder.Decode(body, nil, &ar)
	return &ar, err
}

type Mutator interface {
	Mutate(context.Context, *admissionv1beta1.AdmissionRequest) *admissionv1beta1.AdmissionResponse
}

func HandlerFunc(mutator Mutator) func(w http.ResponseWriter, req *http.Request) {
	runtimeScheme := runtime.NewScheme()
	codecs := serializer.NewCodecFactory(runtimeScheme)
	deserializer := codecs.UniversalDeserializer()

	return func(w http.ResponseWriter, r *http.Request) {
		onBadRequest := func(err error) {
			klog.Errorf("bad request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		onInternalServerError := func(err error) {
			klog.Errorf("internal server error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		if r.Method != http.MethodPost {
			onBadRequest(errors.Errorf("expected method POST, got %s", r.Method))
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			onBadRequest(errors.Errorf("expected Content-Type \"application/json\", got %q", ct))
			return
		}
		body, err := readBody(r, 4*1024*1024)
		if err != nil {
			onBadRequest(err)
			return
		}
		admissionReview, err := decodeAdmissionReview(deserializer, body)
		if err != nil {
			onBadRequest(err)
			return
		}
		if admissionReview.Request == nil {
			onBadRequest(errors.New("empty admissionReview.Request"))
		}
		ctx := context.TODO()
		admissionResponse := mutator.Mutate(ctx, admissionReview.Request)
		if admissionResponse == nil {
			onInternalServerError(errors.New("got nil admissionResponse"))
			return
		}
		replyAdmissionReview := admissionv1beta1.AdmissionReview{}
		replyAdmissionReview.Response = admissionResponse
		replyAdmissionReview.Response.UID = admissionReview.Request.UID
		jsonEncoder := json.NewEncoder(w)
		if err := jsonEncoder.Encode(replyAdmissionReview); err != nil {
			onInternalServerError(err)
		}
	}
}
