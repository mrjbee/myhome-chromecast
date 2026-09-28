package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const baseURL = "https://www.youtube.com/"

var (
	sidPattern      = regexp.MustCompile(`"c","([^"]+)",`)
	gsessionPattern = regexp.MustCompile(`"S","([^"]+)"\]`)
)

type session struct {
	screenID   string
	token      string
	sid        string
	gsessionID string
	nextRID    int
	offset     int
}

type Client struct {
	mu       sync.Mutex
	http     *http.Client
	sessions map[string]*session
}

func NewClient(timeout time.Duration) *Client {
	return &Client{
		http:     &http.Client{Timeout: timeout},
		sessions: make(map[string]*session),
	}
}

func (c *Client) Play(ctx context.Context, screenID, videoID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if current := c.sessions[screenID]; current != nil {
		if err := c.setPlaylist(ctx, current, videoID); err == nil {
			return nil
		}
		delete(c.sessions, screenID)
	}

	current, err := c.bind(ctx, screenID)
	if err != nil {
		return err
	}
	if err := c.setPlaylist(ctx, current, videoID); err != nil {
		return err
	}
	c.sessions[screenID] = current
	return nil
}

func (c *Client) bind(ctx context.Context, screenID string) (*session, error) {
	token, err := c.loungeToken(ctx, screenID)
	if err != nil {
		return nil, err
	}
	params := url.Values{
		"RID": {"0"}, "VER": {"8"}, "CVER": {"1"},
		"TYPE": {"bind"}, "auth_failure_option": {"send_error"},
	}
	form := url.Values{
		"app": {"web"}, "mdx-version": {"3"}, "name": {"myhome-chromecast"},
		"id": {"aaaaaaaaaaaaaaaaaaaaaaaaaa"}, "device": {"REMOTE_CONTROL"},
		"capabilities": {"que,dsdtr,atp"}, "method": {"setPlaylist"},
		"magnaKey": {"cloudPairedDevice"}, "ui": {"false"}, "theme": {"cl"},
		"deviceContext": {"user_agent=myhome-chromecast"}, "os_name": {"linux"},
		"window_width_points": {""}, "window_height_points": {""}, "ms": {""},
		"loungeIdToken": {token},
	}
	response, err := c.post(ctx, baseURL+"api/lounge/bc/bind", params, form, token)
	if err != nil {
		return nil, fmt.Errorf("bind YouTube Lounge session: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read YouTube Lounge bind response: %w", err)
	}
	sidMatch := sidPattern.FindSubmatch(body)
	gsessionMatch := gsessionPattern.FindSubmatch(body)
	if len(sidMatch) != 2 || len(gsessionMatch) != 2 {
		return nil, errors.New("YouTube Lounge bind response has no session identifiers")
	}
	return &session{
		screenID: screenID, token: token,
		sid: string(sidMatch[1]), gsessionID: string(gsessionMatch[1]),
		nextRID: 1,
	}, nil
}

func (c *Client) loungeToken(ctx context.Context, screenID string) (string, error) {
	response, err := c.post(
		ctx,
		baseURL+"api/lounge/pairing/get_lounge_token_batch",
		nil,
		url.Values{"screen_ids": {screenID}},
		"",
	)
	if err != nil {
		return "", fmt.Errorf("get YouTube Lounge token: %w", err)
	}
	defer response.Body.Close()
	var payload struct {
		Screens []struct {
			LoungeToken string `json:"loungeToken"`
		} `json:"screens"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode YouTube Lounge token: %w", err)
	}
	if len(payload.Screens) == 0 || payload.Screens[0].LoungeToken == "" {
		return "", errors.New("YouTube Lounge token response is empty")
	}
	return payload.Screens[0].LoungeToken, nil
}

func (c *Client) setPlaylist(ctx context.Context, current *session, videoID string) error {
	params := url.Values{
		"SID": {current.sid}, "gsessionid": {current.gsessionID},
		"RID": {strconv.Itoa(current.nextRID)}, "VER": {"8"}, "CVER": {"1"},
		"v": {"2"}, "TYPE": {"bind"}, "t": {"1"}, "AID": {"0"}, "CI": {"0"},
		"name": {"myhome-chromecast"}, "id": {"aaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"device": {"REMOTE_CONTROL"}, "loungeIdToken": {current.token},
	}
	form := url.Values{
		"count": {"1"}, "ofs": {strconv.Itoa(current.offset)},
		"req0__sc": {"setPlaylist"}, "req0_listId": {""},
		"req0_currentTime": {"0"}, "req0_currentIndex": {"-1"},
		"req0_audioOnly": {"false"}, "req0_videoId": {videoID},
		"req0_params": {""}, "req0_playerParams": {""},
		"req0_prioritizeMobileSenderPlaybackStateOnConnection": {"true"},
	}
	response, err := c.post(ctx, baseURL+"api/lounge/bc/bind", params, form, current.token)
	if err != nil {
		return fmt.Errorf("set YouTube playlist: %w", err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 16*1024)); err != nil {
		return fmt.Errorf("read YouTube playlist response: %w", err)
	}
	current.nextRID++
	current.offset++
	return nil
}

func (c *Client) post(ctx context.Context, endpoint string, params, form url.Values, token string) (*http.Response, error) {
	if len(params) != 0 {
		endpoint += "?" + params.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", baseURL)
	if token != "" {
		request.Header.Set("X-YouTube-LoungeId-Token", token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return nil, fmt.Errorf("YouTube returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return response, nil
}

func ParseVideoID(rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	host := strings.TrimPrefix(strings.ToLower(parsedURL.Hostname()), "www.")
	var videoID string
	switch host {
	case "youtu.be":
		videoID = strings.Trim(parsedURL.Path, "/")
	case "youtube.com", "m.youtube.com", "music.youtube.com":
		videoID = parsedURL.Query().Get("v")
		if videoID == "" {
			parts := strings.Split(strings.Trim(path.Clean(parsedURL.Path), "/"), "/")
			if len(parts) == 2 && (parts[0] == "embed" || parts[0] == "shorts" || parts[0] == "live") {
				videoID = parts[1]
			}
		}
	}
	if videoID == "" {
		return "", fmt.Errorf("unsupported YouTube URL: %s", rawURL)
	}
	if strings.ContainsAny(videoID, "/?#&") {
		return "", errors.New("invalid YouTube video ID")
	}
	return videoID, nil
}
