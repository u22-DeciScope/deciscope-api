package httpadapter

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"

	"github.com/go-chi/chi/v5"
)

const transcriptSegmentBodyLimitBytes int64 = 64 * 1024

var errEmptyTranscriptText = errors.New("transcript text is empty")

type TranscriptIngestUseCases interface {
	StoreTranscriptSegment(ctx context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error)
	ListTranscriptSegments(ctx context.Context, callID, sessionID string, limit int) ([]domain.TranscriptSegment, error)
}

type TranscriptAPI struct {
	service     TranscriptIngestUseCases
	apiKey      string
	clientToken string
}

func NewTranscriptAPI(service TranscriptIngestUseCases, apiKey string, clientToken ...string) *TranscriptAPI {
	var token string
	if len(clientToken) > 0 {
		token = clientToken[0]
	}
	return &TranscriptAPI{service: service, apiKey: apiKey, clientToken: token}
}

func (api *TranscriptAPI) Store(w http.ResponseWriter, r *http.Request) {
	if !api.authorized(r.Header.Get("X-DeciScope-Api-Key")) {
		log.Printf("Transcript receive failed. reason=unauthorized")
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		log.Printf("Transcript receive failed. reason=unsupported_media_type contentType=%q", r.Header.Get("Content-Type"))
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}

	var request transcriptSegmentRequest
	r.Body = http.MaxBytesReader(w, r.Body, transcriptSegmentBodyLimitBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		if isBodyTooLarge(err) {
			log.Printf("Transcript receive failed. reason=payload_too_large")
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}
		log.Printf("Transcript receive failed. reason=invalid_json error=%v", err)
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if isBodyTooLarge(err) {
			log.Printf("Transcript receive failed. reason=payload_too_large")
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}
		log.Printf("Transcript receive failed. reason=invalid_json_extra_body error=%v", err)
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request body")
		return
	}

	log.Printf("Transcript received. sessionId=%s callId=%s sequenceNo=%d speakerId=%s speakerName=%s textLength=%d",
		request.sessionID(), request.callID(), request.sequenceNo(), request.speakerID(), request.speakerName(), transcriptTextLength(request.text()))

	segment, err := request.toDomain()
	if err != nil {
		if errors.Is(err, errEmptyTranscriptText) {
			log.Printf("Transcript skipped. sessionId=%s callId=%s sequenceNo=%d speakerId=%s speakerName=%s textLength=%d reason=empty_text",
				request.sessionID(), request.callID(), request.sequenceNo(), request.speakerID(), request.speakerName(), transcriptTextLength(request.text()))
			writeJSON(w, http.StatusOK, transcriptSegmentResponse{Status: "skipped", Duplicate: false, EventID: request.eventID()})
			return
		}
		log.Printf("Transcript skipped. sessionId=%s callId=%s sequenceNo=%d speakerId=%s speakerName=%s textLength=%d reason=%v",
			request.sessionID(), request.callID(), request.sequenceNo(), request.speakerID(), request.speakerName(), transcriptTextLength(request.text()), err)
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	result, err := api.service.StoreTranscriptSegment(r.Context(), segment)
	if errors.Is(err, domain.ErrConflict) {
		log.Printf("DB insert error. sessionId=%s eventId=%s callId=%s sequenceNo=%d reason=conflict error=%v", segment.SessionID, segment.EventID, segment.CallID, segment.SequenceNo, err)
		writeError(w, http.StatusConflict, "conflict", "transcript segment conflict")
		return
	}
	if err != nil {
		log.Printf("DB insert error. sessionId=%s eventId=%s callId=%s sequenceNo=%d error=%v", segment.SessionID, segment.EventID, segment.CallID, segment.SequenceNo, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	switch result.Status {
	case domain.TranscriptSegmentCreated:
		log.Printf("Transcript saved. sessionId=%s eventId=%s callId=%s sequenceNo=%d speakerId=%s speakerName=%s textLength=%d",
			segment.SessionID, segment.EventID, segment.CallID, segment.SequenceNo, segment.SpeakerID, segment.SpeakerName, transcriptTextLength(segment.Text))
		writeJSON(w, http.StatusCreated, transcriptSegmentResponse{Status: string(result.Status), Duplicate: false, EventID: segment.EventID})
	case domain.TranscriptSegmentAlreadyExists:
		log.Printf("Transcript skipped. sessionId=%s eventId=%s callId=%s sequenceNo=%d speakerId=%s speakerName=%s textLength=%d reason=duplicate",
			segment.SessionID, segment.EventID, segment.CallID, segment.SequenceNo, segment.SpeakerID, segment.SpeakerName, transcriptTextLength(segment.Text))
		writeJSON(w, http.StatusOK, transcriptSegmentResponse{Status: string(result.Status), Duplicate: true, EventID: segment.EventID})
	default:
		log.Printf("Store transcript segment returned unknown status. sessionId=%s eventId=%s callId=%s sequenceNo=%d", segment.SessionID, segment.EventID, segment.CallID, segment.SequenceNo)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func (api *TranscriptAPI) List(w http.ResponseWriter, r *http.Request) {
	if !api.authorizedClient(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	limit, err := parseTranscriptLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	callID := strings.TrimSpace(r.URL.Query().Get("callId"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	segments, err := api.service.ListTranscriptSegments(r.Context(), callID, sessionID, limit)
	if err != nil {
		log.Printf("List transcript segments failed. callId=%s sessionId=%s limit=%d error=%v", callID, sessionID, limit, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	log.Printf("List transcript segments response. callId=%s sessionId=%s limit=%d count=%d", callID, sessionID, limit, len(segments))
	writeJSON(w, http.StatusOK, transcriptSegmentListResponse{Items: transcriptSegmentItems(segments)})
}

func (api *TranscriptAPI) ListByMeetingSession(w http.ResponseWriter, r *http.Request) {
	if !api.authorizedClient(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	limit, err := parseTranscriptLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sessionID := strings.TrimSpace(chi.URLParam(r, "session_id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "session_id is required")
		return
	}
	segments, err := api.service.ListTranscriptSegments(r.Context(), "", sessionID, limit)
	if err != nil {
		log.Printf("List transcript segments failed. sessionId=%s limit=%d error=%v", sessionID, limit, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	log.Printf("List transcript segments response. sessionId=%s limit=%d count=%d", sessionID, limit, len(segments))
	writeJSON(w, http.StatusOK, transcriptSegmentListResponse{Items: transcriptSegmentItems(segments)})
}

func (api *TranscriptAPI) authorized(value string) bool {
	return authorizedSecret(value, api.apiKey)
}

func (api *TranscriptAPI) authorizedClient(r *http.Request) bool {
	if strings.TrimSpace(api.clientToken) == "" {
		return true
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		token = bearerToken(r.Header.Get("Authorization"))
	}
	return authorizedSecret(token, api.clientToken)
}

func authorizedSecret(value, secret string) bool {
	if value == "" || secret == "" {
		return false
	}
	got := sha256.Sum256([]byte(value))
	want := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

type transcriptSegmentRequest struct {
	SessionID               string `json:"sessionId"`
	SessionIDSnake          string `json:"session_id"`
	EventID                 string `json:"eventId"`
	EventIDSnake            string `json:"event_id"`
	CallID                  string `json:"callId"`
	CallIDSnake             string `json:"call_id"`
	SequenceNo              int64  `json:"sequenceNo"`
	SequenceNoSnake         int64  `json:"sequence_no"`
	SpeakerID               string `json:"speakerId"`
	SpeakerIDSnake          string `json:"speaker_id"`
	SpeakerLabel            string `json:"speakerLabel"`
	SpeakerLabelSnake       string `json:"speaker_label"`
	SpeakerName             string `json:"speakerName"`
	SpeakerNameSnake        string `json:"speaker_name"`
	SpeakerDisplayName      string `json:"speakerDisplayName"`
	SpeakerDisplayNameSnake string `json:"speaker_display_name"`
	ParticipantName         string `json:"participantName"`
	ParticipantNameSnake    string `json:"participant_name"`
	UserName                string `json:"userName"`
	UserNameSnake           string `json:"user_name"`
	RecognizedAtUTC         string `json:"recognizedAtUtc"`
	RecognizedAtUTCAlt      string `json:"recognizedAtUTC"`
	RecognizedAtUTCSnake    string `json:"recognized_at_utc"`
	OffsetTicks             int64  `json:"offsetTicks"`
	OffsetTicksSnake        int64  `json:"offset_ticks"`
	DurationTicks           int64  `json:"durationTicks"`
	DurationTicksSnake      int64  `json:"duration_ticks"`
	Text                    string `json:"text"`
}

type transcriptSegmentResponse struct {
	Status    string `json:"status"`
	Duplicate bool   `json:"duplicate"`
	EventID   string `json:"eventId"`
}

type transcriptSegmentListResponse struct {
	Items []transcriptSegmentItem `json:"items"`
}

type transcriptSegmentItem struct {
	SessionID       string `json:"sessionId,omitempty"`
	EventID         string `json:"eventId"`
	CallID          string `json:"callId"`
	SequenceNo      int64  `json:"sequenceNo"`
	SpeakerID       string `json:"speakerId,omitempty"`
	SpeakerName     string `json:"speakerName,omitempty"`
	RecognizedAtUTC string `json:"recognizedAtUtc"`
	OffsetTicks     int64  `json:"offsetTicks"`
	DurationTicks   int64  `json:"durationTicks"`
	Text            string `json:"text"`
	ReceivedAtUTC   string `json:"receivedAtUtc"`
}

func (request transcriptSegmentRequest) toDomain() (domain.TranscriptSegment, error) {
	sessionID := request.sessionID()
	callID := request.callID()
	if callID == "" {
		return domain.TranscriptSegment{}, fmt.Errorf("callId is required")
	}
	sequenceNo := request.sequenceNo()
	if sequenceNo < 1 {
		return domain.TranscriptSegment{}, fmt.Errorf("sequenceNo must be 1 or greater")
	}
	eventID := request.eventID()
	if eventID == "" {
		eventID = generatedTranscriptEventID(sessionID, callID, sequenceNo)
	}
	recognizedAtText := request.recognizedAtUTC()
	recognizedAt := time.Now().UTC()
	if recognizedAtText != "" {
		parsed, err := time.Parse(time.RFC3339Nano, recognizedAtText)
		if err != nil {
			return domain.TranscriptSegment{}, fmt.Errorf("recognizedAtUtc must be RFC3339")
		}
		if _, offset := parsed.Zone(); offset != 0 {
			return domain.TranscriptSegment{}, fmt.Errorf("recognizedAtUtc must use UTC offset")
		}
		recognizedAt = parsed.UTC()
	}
	offsetTicks := request.offsetTicks()
	durationTicks := request.durationTicks()
	if offsetTicks < 0 {
		return domain.TranscriptSegment{}, fmt.Errorf("offsetTicks must be 0 or greater")
	}
	if durationTicks < 0 {
		return domain.TranscriptSegment{}, fmt.Errorf("durationTicks must be 0 or greater")
	}
	text := request.text()
	if text == "" {
		return domain.TranscriptSegment{}, errEmptyTranscriptText
	}
	return domain.TranscriptSegment{
		SessionID:       sessionID,
		EventID:         eventID,
		CallID:          callID,
		SequenceNo:      sequenceNo,
		SpeakerID:       request.speakerID(),
		SpeakerName:     request.speakerName(),
		RecognizedAtUTC: recognizedAt.UTC(),
		OffsetTicks:     offsetTicks,
		DurationTicks:   durationTicks,
		Text:            text,
	}, nil
}

func parseTranscriptLimit(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 {
		return 0, fmt.Errorf("limit must be 1 or greater")
	}
	if limit > 500 {
		return 500, nil
	}
	return limit, nil
}

func bearerToken(value string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, prefix))
}

func transcriptSegmentItems(segments []domain.TranscriptSegment) []transcriptSegmentItem {
	items := make([]transcriptSegmentItem, 0, len(segments))
	for _, segment := range segments {
		items = append(items, transcriptSegmentItem{
			SessionID:       segment.SessionID,
			EventID:         segment.EventID,
			CallID:          segment.CallID,
			SequenceNo:      segment.SequenceNo,
			SpeakerID:       segment.SpeakerID,
			SpeakerName:     segment.SpeakerName,
			RecognizedAtUTC: segment.RecognizedAtUTC.UTC().Format(time.RFC3339Nano),
			OffsetTicks:     segment.OffsetTicks,
			DurationTicks:   segment.DurationTicks,
			Text:            segment.Text,
			ReceivedAtUTC:   segment.ReceivedAtUTC.UTC().Format(time.RFC3339Nano),
		})
	}
	return items
}

func (request transcriptSegmentRequest) speakerName() string {
	for _, value := range []string{
		request.SpeakerName,
		request.SpeakerNameSnake,
		request.SpeakerLabel,
		request.SpeakerLabelSnake,
		request.SpeakerDisplayName,
		request.SpeakerDisplayNameSnake,
		request.ParticipantName,
		request.ParticipantNameSnake,
		request.UserName,
		request.UserNameSnake,
	} {
		if label := strings.TrimSpace(value); label != "" {
			return label
		}
	}
	return ""
}

func (request transcriptSegmentRequest) sessionID() string {
	return firstNonBlankString(request.SessionID, request.SessionIDSnake)
}

func (request transcriptSegmentRequest) eventID() string {
	return firstNonBlankString(request.EventID, request.EventIDSnake)
}

func (request transcriptSegmentRequest) callID() string {
	return firstNonBlankString(request.CallID, request.CallIDSnake)
}

func (request transcriptSegmentRequest) sequenceNo() int64 {
	return firstPositiveInt64(request.SequenceNo, request.SequenceNoSnake)
}

func (request transcriptSegmentRequest) speakerID() string {
	return firstNonBlankString(request.SpeakerID, request.SpeakerIDSnake)
}

func (request transcriptSegmentRequest) recognizedAtUTC() string {
	return firstNonBlankString(request.RecognizedAtUTC, request.RecognizedAtUTCAlt, request.RecognizedAtUTCSnake)
}

func (request transcriptSegmentRequest) offsetTicks() int64 {
	return firstPresentInt64(request.OffsetTicks, request.OffsetTicksSnake)
}

func (request transcriptSegmentRequest) durationTicks() int64 {
	return firstPresentInt64(request.DurationTicks, request.DurationTicksSnake)
}

func (request transcriptSegmentRequest) text() string {
	return strings.TrimSpace(request.Text)
}

func firstNonBlankString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstPresentInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func generatedTranscriptEventID(sessionID, callID string, sequenceNo int64) string {
	if sessionID != "" {
		return fmt.Sprintf("transcript:%s:%s:%d", sessionID, callID, sequenceNo)
	}
	return fmt.Sprintf("transcript:%s:%d", callID, sequenceNo)
}

func transcriptTextLength(value string) int {
	return len([]rune(strings.TrimSpace(value)))
}

func isJSONContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func isBodyTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}
