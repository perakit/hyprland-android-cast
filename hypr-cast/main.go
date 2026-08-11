package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	currentLock sync.RWMutex
	latestFrame []byte
	resW        = 1600
	resH        = 900

	clients     = make(map[chan []byte]bool)
	clientsLock sync.Mutex

	castPIN        string
	activeSessions = make(map[string]time.Time)
	sessionLock    sync.RWMutex

	failedAttempts = make(map[string]int)
	lockoutUntil   = make(map[string]time.Time)
	rateLock       sync.Mutex
)

const (
	maxFailedAttempts = 5
	lockoutDuration   = 5 * time.Minute
)

func generateRandomPIN() string {
	b := make([]byte, 3)
	rand.Read(b)
	num := (int(b[0])<<16 | int(b[1])<<8 | int(b[2])) % 900000 + 100000
	return strconv.Itoa(num)
}

func generateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func notifyPIN(pin string) {
	cmd := exec.Command("notify-send", "-u", "critical", "-i", "dialog-password", "Hypr-Cast PIN", fmt.Sprintf("Authentication PIN: %s", pin))
	cmd.Run()
}

var (
	lastStreamNotify = make(map[string]time.Time)
	streamNotifyLock sync.Mutex
)

func notifyConnected(ip string) {
	log.Printf("SECURITY EVENT: Client authenticated from IP %s", ip)
	exec.Command("notify-send", "-u", "normal", "-i", "network-wireless", "Hypr-Cast Connected", fmt.Sprintf("Device connected from IP: %s", ip)).Run()
}

func notifyStreamStarted(ip string) {
	streamNotifyLock.Lock()
	last, exists := lastStreamNotify[ip]
	now := time.Now()
	if !exists || now.Sub(last) > 30*time.Second {
		lastStreamNotify[ip] = now
		streamNotifyLock.Unlock()
		log.Printf("SECURITY EVENT: Stream viewing started by IP %s", ip)
		exec.Command("notify-send", "-u", "normal", "-i", "video-display", "Hypr-Cast Stream Active", fmt.Sprintf("Screen casting active for IP: %s", ip)).Run()
	} else {
		streamNotifyLock.Unlock()
	}
}

func getClientIP(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	if ip != "" {
		parts := strings.Split(ip, ",")
		return strings.TrimSpace(parts[0])
	}
	ip = r.Header.Get("X-Real-IP")
	if ip != "" {
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func checkRateLimit(ip string) (isLocked bool, retryAfterSeconds int) {
	rateLock.Lock()
	defer rateLock.Unlock()

	until, exists := lockoutUntil[ip]
	if exists {
		if time.Now().Before(until) {
			remaining := int(time.Until(until).Seconds()) + 1
			return true, remaining
		}
		delete(lockoutUntil, ip)
		delete(failedAttempts, ip)
	}
	return false, 0
}

func recordFailedAttempt(ip string) (isNowLocked bool, attemptsLeft int, retryAfterSeconds int) {
	rateLock.Lock()
	defer rateLock.Unlock()

	failedAttempts[ip]++
	count := failedAttempts[ip]
	if count >= maxFailedAttempts {
		lockoutUntil[ip] = time.Now().Add(lockoutDuration)
		delete(failedAttempts, ip)
		log.Printf("SECURITY ALERT: IP %s locked out for %v after %d failed attempts", ip, lockoutDuration, count)
		exec.Command("notify-send", "-u", "critical", "-i", "security-high", "Hypr-Cast Security Alert", fmt.Sprintf("IP %s locked out (brute-force prevention)", ip)).Run()
		return true, 0, int(lockoutDuration.Seconds())
	}
	return false, maxFailedAttempts - count, 0
}

func resetFailedAttempts(ip string) {
	rateLock.Lock()
	defer rateLock.Unlock()
	delete(failedAttempts, ip)
	delete(lockoutUntil, ip)
}

func isAuth(r *http.Request) bool {
	cookie, err := r.Cookie("hyprcast_session")
	if err == nil && cookie.Value != "" {
		sessionLock.RLock()
		expiry, ok := activeSessions[cookie.Value]
		sessionLock.RUnlock()
		if ok && time.Now().Before(expiry) {
			return true
		}
	}
	queryAuth := r.URL.Query().Get("auth")
	if queryAuth != "" {
		sessionLock.RLock()
		expiry, ok := activeSessions[queryAuth]
		sessionLock.RUnlock()
		if ok && time.Now().Before(expiry) {
			return true
		}
	}
	return false
}

func createSession(w http.ResponseWriter) string {
	token := generateToken()
	sessionLock.Lock()
	activeSessions[token] = time.Now().Add(7 * 24 * time.Hour)
	sessionLock.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "hyprcast_session",
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(7 * 24 * time.Hour),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuth(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

const loginPage = `<!DOCTYPE html>
<html>
<head>
    <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
    <title>Hypr-Cast Authentication</title>
    <style>
        * { box-sizing: border-box; }
        html, body {
            margin: 0; padding: 0;
            width: 100vw; height: 100vh;
            background-color: #0b0b10;
            color: #cdd6f4;
            font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            display: flex; align-items: center; justify-content: center;
            touch-action: manipulation;
        }
        .card {
            background: #181825;
            border: 1px solid #313244;
            border-radius: 16px;
            padding: 32px;
            width: 90%;
            max-width: 360px;
            box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
            text-align: center;
        }
        h2 {
            margin: 0 0 8px 0;
            font-size: 22px;
            color: #cba6f7;
        }
        p {
            margin: 0 0 24px 0;
            font-size: 14px;
            color: #a6adc8;
        }
        .pin-display {
            width: 100%;
            padding: 14px;
            font-size: 24px;
            letter-spacing: 6px;
            text-align: center;
            background: #11111b;
            border: 1px solid #45475a;
            border-radius: 10px;
            color: #f5e0dc;
            margin-bottom: 16px;
            outline: none;
        }
        .pin-display:focus {
            border-color: #cba6f7;
        }
        .keypad {
            display: grid;
            grid-template-columns: repeat(3, 1fr);
            gap: 10px;
            margin-bottom: 20px;
        }
        .key-btn {
            background: #313244;
            color: #cdd6f4;
            border: none;
            border-radius: 10px;
            padding: 16px;
            font-size: 20px;
            font-weight: bold;
            cursor: pointer;
            user-select: none;
            touch-action: manipulation;
            transition: background 0.15s ease;
        }
        .key-btn:active {
            background: #45475a;
        }
        .btn-submit {
            width: 100%;
            background: #cba6f7;
            color: #11111b;
            border: none;
            border-radius: 10px;
            padding: 14px;
            font-size: 16px;
            font-weight: bold;
            cursor: pointer;
            transition: background 0.15s ease;
        }
        .btn-submit:active {
            background: #b4befe;
        }
        .error {
            color: #f38ba8;
            font-size: 13px;
            margin-top: 12px;
            min-height: 18px;
        }
    </style>
</head>
<body>
    <div class="card">
        <h2>Hypr-Cast</h2>
        <p>Enter PIN to connect</p>
        <form id="loginForm" onsubmit="handleLogin(event)">
            <input type="password" id="pinInput" class="pin-display" placeholder="••••" autocomplete="off" autofocus inputmode="numeric" />
            <div class="keypad">
                <button type="button" class="key-btn" onclick="press('1')">1</button>
                <button type="button" class="key-btn" onclick="press('2')">2</button>
                <button type="button" class="key-btn" onclick="press('3')">3</button>
                <button type="button" class="key-btn" onclick="press('4')">4</button>
                <button type="button" class="key-btn" onclick="press('5')">5</button>
                <button type="button" class="key-btn" onclick="press('6')">6</button>
                <button type="button" class="key-btn" onclick="press('7')">7</button>
                <button type="button" class="key-btn" onclick="press('8')">8</button>
                <button type="button" class="key-btn" onclick="press('9')">9</button>
                <button type="button" class="key-btn" onclick="clearPin()">C</button>
                <button type="button" class="key-btn" onclick="press('0')">0</button>
                <button type="button" class="key-btn" onclick="backspace()">⌫</button>
            </div>
            <button type="submit" class="btn-submit">Connect</button>
            <div id="error" class="error"></div>
        </form>
    </div>
    <script>
        const input = document.getElementById('pinInput');
        const errDiv = document.getElementById('error');

        function press(digit) {
            input.value += digit;
            errDiv.innerText = '';
        }
        function clearPin() {
            input.value = '';
            errDiv.innerText = '';
        }
        function backspace() {
            input.value = input.value.slice(0, -1);
            errDiv.innerText = '';
        }

        async function handleLogin(e) {
            e.preventDefault();
            errDiv.innerText = '';
            const pin = input.value.trim();
            if (!pin) return;

            try {
                const formData = new URLSearchParams();
                formData.append('pin', pin);
                const res = await fetch('/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: formData
                });
                const data = await res.json();
                if (res.ok && data.success) {
                    window.location.href = '/';
                } else {
                    errDiv.innerText = data.error || 'Invalid PIN';
                    input.value = '';
                }
            } catch (err) {
                errDiv.innerText = 'Connection error';
            }
        }
    </script>
</body>
</html>
`

func loginHandler(w http.ResponseWriter, r *http.Request) {
	clientIP := getClientIP(r)

	if r.Method == http.MethodGet {
		if isAuth(r) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(loginPage))
		return
	}

	if r.Method == http.MethodPost {
		isLocked, retrySec := checkRateLimit(clientIP)
		if isLocked {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(fmt.Sprintf(`{"success":false,"error":"Too many failed attempts. Try again in %d seconds."}`, retrySec)))
			return
		}

		r.ParseForm()
		submittedPIN := strings.TrimSpace(r.FormValue("pin"))
		if submittedPIN == castPIN {
			resetFailedAttempts(clientIP)
			createSession(w)
			notifyConnected(clientIP)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true}`))
			return
		}

		isNowLocked, attemptsLeft, retrySec := recordFailedAttempt(clientIP)
		w.Header().Set("Content-Type", "application/json")
		if isNowLocked {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(fmt.Sprintf(`{"success":false,"error":"Too many failed attempts. Locked out for %d seconds."}`, retrySec)))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(fmt.Sprintf(`{"success":false,"error":"Invalid PIN (%d attempts remaining)"}`, attemptsLeft)))
		}
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("hyprcast_session")
	if err == nil && cookie.Value != "" {
		sessionLock.Lock()
		delete(activeSessions, cookie.Value)
		sessionLock.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "hyprcast_session",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func registerClient() chan []byte {
	ch := make(chan []byte, 2)
	clientsLock.Lock()
	clients[ch] = true
	clientsLock.Unlock()
	return ch
}

func unregisterClient(ch chan []byte) {
	clientsLock.Lock()
	delete(clients, ch)
	clientsLock.Unlock()
	close(ch)
}

func broadcastFrame(frame []byte) {
	currentLock.Lock()
	latestFrame = frame
	currentLock.Unlock()

	clientsLock.Lock()
	defer clientsLock.Unlock()

	for ch := range clients {
		select {
		case ch <- frame:
		default: // Skip frame if client network buffer is full
		}
	}
}

func getHeadlessMonitorName() string {
	out, err := exec.Command("hyprctl", "monitors").Output()
	if err != nil {
		return "HEADLESS-3"
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.Contains(line, "Monitor HEADLESS-") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return "HEADLESS-3"
}

func getHeadlessOffset() (int, int) {
	out, err := exec.Command("hyprctl", "monitors").Output()
	if err != nil {
		return 3840, 0
	}
	lines := strings.Split(string(out), "\n")
	insideHeadless := false
	for _, line := range lines {
		if strings.Contains(line, "Monitor HEADLESS-") {
			insideHeadless = true
			continue
		}
		if insideHeadless && strings.Contains(line, " Monitor ") {
			break
		}
		if insideHeadless && strings.Contains(line, " at ") {
			parts := strings.Split(line, " at ")
			if len(parts) >= 2 {
				coords := strings.Fields(parts[1])
				if len(coords) >= 1 {
					xy := strings.Split(coords[0], "x")
					if len(xy) == 2 {
						x, _ := strconv.Atoi(xy[0])
						y, _ := strconv.Atoi(xy[1])
						return x, y
					}
				}
			}
		}
	}
	return 3840, 0
}

func getBinaryPath(name string) string {
	execPath, err := os.Executable()
	if err == nil {
		p := filepath.Join(filepath.Dir(execPath), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join("/home/perakit/hypr-cast", name)
}

func captureLoop() {
	for {
		log.Println("Starting wlr_jpeg (persistent sRGB 4:4:4 libjpeg-turbo capture)...")
		cmd := exec.Command(getBinaryPath("wlr_jpeg"))
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("wlr_jpeg stdout pipe error: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if err := cmd.Start(); err != nil {
			log.Printf("wlr_jpeg start error: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		lenBuf := make([]byte, 4)
		for {
			_, err := io.ReadFull(stdout, lenBuf)
			if err != nil {
				log.Printf("wlr_jpeg read len error: %v", err)
				break
			}

			jpegLen := binary.LittleEndian.Uint32(lenBuf)
			if jpegLen == 0 || jpegLen > 10000000 {
				log.Printf("Invalid jpegLen: %d", jpegLen)
				break
			}

			jpegBytes := make([]byte, jpegLen)
			_, err = io.ReadFull(stdout, jpegBytes)
			if err != nil {
				log.Printf("wlr_jpeg read frame error: %v", err)
				break
			}

			broadcastFrame(jpegBytes)
		}

		cmd.Process.Kill()
		cmd.Wait()
		time.Sleep(500 * time.Millisecond)
	}
}

const htmlPage = `<!DOCTYPE html>
<html>
<head>
    <meta name="viewport" content="width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no">
    <style>
        html, body {
            margin: 0; padding: 0;
            width: 100vw; height: 100vh;
            background-color: #000000;
            display: flex; align-items: center; justify-content: center;
            overflow: hidden; touch-action: none; outline: none;
        }
        img {
            width: 100vw; height: 100vh;
            object-fit: contain; display: block;
            touch-action: none; user-select: none;
            -webkit-user-select: none; outline: none;
        }
    </style>
</head>
<body tabindex="0">
    <img id="stream" src="/stream" alt="Cast Stream" tabindex="0" />
    <script>
        const img = document.getElementById('stream');
        document.body.focus();

        let targetResW = 1600;
        let targetResH = 900;
        let isReconnecting = false;

        function reconnectStream() {
            if (isReconnecting) return;
            isReconnecting = true;
            img.src = '/stream?t=' + Date.now();
            setTimeout(() => { isReconnecting = false; }, 300);
        }

        function handleAuthError(res) {
            if (res.status === 401) {
                window.location.href = '/login';
            }
            return res;
        }

        function adaptResolution() {
            const vw = window.innerWidth || document.documentElement.clientWidth || 1600;
            const vh = window.innerHeight || document.documentElement.clientHeight || 900;
            const aspect = vw / vh;
            
            targetResW = 1600;
            targetResH = Math.round(1600 / aspect);
            
            fetch('/resize?w=' + targetResW + '&h=' + targetResH).then(handleAuthError).catch(() => {});
        }

        if (document.readyState === 'complete' || document.readyState === 'interactive') {
            adaptResolution();
        } else {
            window.addEventListener('DOMContentLoaded', adaptResolution);
        }

        let resizeTimeout;
        window.addEventListener('resize', () => {
            clearTimeout(resizeTimeout);
            resizeTimeout = setTimeout(adaptResolution, 300);
        });

        document.addEventListener('visibilitychange', () => {
            if (document.visibilityState === 'visible') {
                reconnectStream();
                adaptResolution();
            }
        });

        window.addEventListener('focus', () => {
            reconnectStream();
            adaptResolution();
        });

        window.addEventListener('pageshow', () => {
            reconnectStream();
            adaptResolution();
        });

        window.addEventListener('online', () => {
            reconnectStream();
        });

        img.onerror = () => {
            fetch('/stream').then(res => {
                if (res.status === 401) window.location.href = '/login';
                else setTimeout(reconnectStream, 500);
            }).catch(() => setTimeout(reconnectStream, 500));
        };

        function getNormalizedCoords(clientX, clientY) {
            const rect = img.getBoundingClientRect();
            const nw = img.naturalWidth || targetResW || 1600;
            const nh = img.naturalHeight || targetResH || 900;
            const naturalRatio = nw / nh;
            const elementRatio = rect.width / rect.height;

            let renderWidth, renderHeight, offsetX, offsetY;

            if (elementRatio > naturalRatio) {
                renderHeight = rect.height;
                renderWidth = renderHeight * naturalRatio;
                offsetX = (rect.width - renderWidth) / 2;
                offsetY = 0;
            } else {
                renderWidth = rect.width;
                renderHeight = renderWidth / naturalRatio;
                offsetX = 0;
                offsetY = (rect.height - renderHeight) / 2;
            }

            const clickX = clientX - rect.left - offsetX;
            const clickY = clientY - rect.top - offsetY;

            const x = Math.max(0, Math.min(1, clickX / renderWidth));
            const y = Math.max(0, Math.min(1, clickY / renderHeight));

            return { x, y };
        }

        function sendEvent(clientX, clientY, action) {
            const { x, y } = getNormalizedCoords(clientX, clientY);
            fetch('/touch?x=' + x.toFixed(4) + '&y=' + y.toFixed(4) + '&action=' + action).then(handleAuthError).catch(() => {});
        }

        let isDragging = false;
        let lastMoveTime = 0;

        img.addEventListener('pointerdown', (e) => {
            e.preventDefault();
            document.body.focus();
            try { img.setPointerCapture(e.pointerId); } catch (_) {}
            isDragging = false;
            sendEvent(e.clientX, e.clientY, 'move');
        });

        img.addEventListener('pointermove', (e) => {
            e.preventDefault();
            const now = Date.now();
            if (now - lastMoveTime > 8) {
                lastMoveTime = now;
                if (e.buttons > 0) {
                    if (!isDragging) {
                        isDragging = true;
                        sendEvent(e.clientX, e.clientY, 'down');
                    }
                    sendEvent(e.clientX, e.clientY, 'move');
                } else {
                    sendEvent(e.clientX, e.clientY, 'move');
                }
            }
        });

        img.addEventListener('pointerup', (e) => {
            e.preventDefault();
            try { img.releasePointerCapture(e.pointerId); } catch (_) {}
            if (isDragging) {
                sendEvent(e.clientX, e.clientY, 'up');
                isDragging = false;
            }
        });

        img.addEventListener('click', (e) => {
            sendEvent(e.clientX, e.clientY, 'click');
        });

        img.addEventListener('contextmenu', (e) => {
            e.preventDefault();
            sendEvent(e.clientX, e.clientY, 'rightclick');
        });

        window.onAndroidShortcut = (key, code, ctrl, alt, shift, meta) => {
            fetch('/key?key=' + encodeURIComponent(key) + '&code=' + encodeURIComponent(code) + '&ctrl=' + ctrl + '&alt=' + alt + '&shift=' + shift + '&meta=' + meta + '&action=down').then(handleAuthError).catch(() => {});
        };

        window.addEventListener('keydown', (e) => {
            fetch('/debug?type=keydown&key=' + encodeURIComponent(e.key) + '&code=' + encodeURIComponent(e.code) + '&ctrl=' + (e.ctrlKey?1:0) + '&alt=' + (e.altKey?1:0) + '&shift=' + (e.shiftKey?1:0) + '&meta=' + (e.metaKey?1:0)).then(handleAuthError).catch(() => {});

            const modifierKeys = ['Control', 'ControlLeft', 'ControlRight', 'NumLock', 'NumpadClear', 'Clear', 'Shift', 'ShiftLeft', 'ShiftRight', 'Alt', 'AltLeft', 'AltRight', 'Meta', 'MetaLeft', 'MetaRight', 'OS', 'Unidentified'];

            if (modifierKeys.includes(e.key) || e.ctrlKey || e.metaKey || e.altKey) {
                e.preventDefault();
            }

            if (modifierKeys.includes(e.key) && !e.code.startsWith('Key') && !e.code.startsWith('Digit')) {
                return;
            }

            let keyName = e.key;

            if (e.code.startsWith('Key')) {
                keyName = e.code.replace('Key', '').toLowerCase();
            } else if (e.code.startsWith('Digit')) {
                keyName = e.code.replace('Digit', '');
            }

            if (modifierKeys.includes(keyName)) {
                return;
            }

            const ctrl = e.ctrlKey ? '1' : '0';
            const alt = e.altKey ? '1' : '0';
            const shift = e.shiftKey ? '1' : '0';
            const meta = e.metaKey ? '1' : '0';
            fetch('/key?key=' + encodeURIComponent(keyName) + '&code=' + encodeURIComponent(e.code) + '&ctrl=' + ctrl + '&alt=' + alt + '&shift=' + shift + '&meta=' + meta + '&action=down').then(handleAuthError).catch(() => {});
        });
    </script>
</body>
</html>
`

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if !isAuth(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(htmlPage))
}

func streamHandler(w http.ResponseWriter, r *http.Request) {
	clientIP := getClientIP(r)
	notifyStreamStarted(clientIP)

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := registerClient()
	defer unregisterClient(ch)

	for {
		select {
		case frame, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame))
			w.Write(frame)
			w.Write([]byte("\r\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func resizeHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	nw, _ := strconv.Atoi(q.Get("w"))
	nh, _ := strconv.Atoi(q.Get("h"))

	if nw <= 0 {
		nw = 1600
	}
	if nh <= 0 {
		nh = 900
	}

	nw = (nw / 2) * 2
	nh = (nh / 2) * 2

	currentLock.Lock()
	if nw != resW || nh != resH {
		resW = nw
		resH = nh

		mon := getHeadlessMonitorName()
		offsetX, offsetY := getHeadlessOffset()
		log.Printf("RESIZING %s TO (%dx%d) at offset (%d,%d)...\n", mon, resW, resH, offsetX, offsetY)
		cmd := fmt.Sprintf("hyprctl keyword monitor %s,%dx%d@60,%dx%d,1", mon, resW, resH, offsetX, offsetY)
		exec.Command("sh", "-c", cmd).Run()
	}
	currentLock.Unlock()

	w.Write([]byte("OK"))
}

func touchHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	xStr := q.Get("x")
	yStr := q.Get("y")
	action := q.Get("action")
	if action == "" {
		action = "click"
	}

	x, _ := strconv.ParseFloat(xStr, 64)
	y, _ := strconv.ParseFloat(yStr, 64)

	currentLock.RLock()
	cw := resW
	ch := resH
	currentLock.RUnlock()

	offsetX, offsetY := getHeadlessOffset()
	absX := offsetX + int(x*float64(cw))
	absY := offsetY + int(y*float64(ch))

	go func() {
		mon := getHeadlessMonitorName()
		exec.Command("hyprctl", "dispatch", "focusmonitor", mon).Run()
		exec.Command("hyprctl", "dispatch", "movecursor", strconv.Itoa(absX), strconv.Itoa(absY)).Run()
		exec.Command(getBinaryPath("wlr_click"), xStr, yStr, strconv.Itoa(cw), strconv.Itoa(ch), action).Run()
	}()

	w.Write([]byte("OK"))
}

func keyHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := q.Get("key")
	code := q.Get("code")
	ctrl := q.Get("ctrl") == "1"
	alt := q.Get("alt") == "1"
	shift := q.Get("shift") == "1"
	meta := q.Get("meta") == "1"

	log.Printf("KEY RECEIVED: key=%q, code=%q, ctrl=%v, alt=%v, shift=%v, meta=%v", key, code, ctrl, alt, shift, meta)

	if key != "" {
		go func(k, c string, isCtrl, isAlt, isShift, isMeta bool) {
			mon := getHeadlessMonitorName()
			exec.Command("hyprctl", "dispatch", "focusmonitor", mon).Run()

			// When modifier keys (CTRL / ALT / SUPER) are active, use Hyprland's native compositor shortcut dispatcher
			if isCtrl || isAlt || isMeta {
				var mods []string
				if isCtrl {
					mods = append(mods, "CTRL")
				}
				if isAlt {
					mods = append(mods, "ALT")
				}
				if isMeta {
					mods = append(mods, "SUPER")
				}
				if isShift {
					mods = append(mods, "SHIFT")
				}

				modStr := strings.Join(mods, " ")
				keyUpper := strings.ToUpper(k)
				if k == " " {
					keyUpper = "SPACE"
				}

				cmd := exec.Command("hyprctl", "dispatch", "sendshortcut", modStr+", "+keyUpper+", activewindow")
				out, err := cmd.CombinedOutput()
				if err != nil {
					log.Printf("sendshortcut ERROR: %v, output: %s", err, string(out))
				} else {
					log.Printf("sendshortcut EXECUTED: %s, %s, activewindow", modStr, keyUpper)
				}
				return
			}

			// Plain typing (no CTRL / ALT / SUPER modifier) uses wtype
			var args []string
			if isShift {
				args = append(args, "-M", "shift")
			}

			switch k {
			case "Enter":
				args = append(args, "-k", "Return")
			case "Backspace":
				args = append(args, "-k", "BackSpace")
			case "Tab":
				args = append(args, "-k", "Tab")
			case "Escape":
				args = append(args, "-k", "Escape")
			case "ArrowLeft":
				args = append(args, "-k", "Left")
			case "ArrowRight":
				args = append(args, "-k", "Right")
			case "ArrowUp":
				args = append(args, "-k", "Up")
			case "ArrowDown":
				args = append(args, "-k", "Down")
			case "Delete":
				args = append(args, "-k", "Delete")
			case "Home":
				args = append(args, "-k", "Home")
			case "End":
				args = append(args, "-k", "End")
			case "PageUp":
				args = append(args, "-k", "Prior")
			case "PageDown":
				args = append(args, "-k", "Next")
			default:
				if len(k) == 1 {
					args = append(args, k)
				}
			}

			if isShift {
				args = append(args, "-m", "shift")
			}

			if len(args) > 0 {
				cmd := exec.Command("wtype", args...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					log.Printf("wtype ERROR: %v, output: %s (args: %v)", err, string(out), args)
				} else {
					log.Printf("wtype EXECUTED: wtype %s", strings.Join(args, " "))
				}
			}
		}(key, code, ctrl, alt, shift, meta)
	}

	w.Write([]byte("OK"))
}

func debugHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	log.Printf("[RAW JS EVENT] type=%s key=%q code=%q ctrl=%s alt=%s shift=%s meta=%s",
		q.Get("type"), q.Get("key"), q.Get("code"), q.Get("ctrl"), q.Get("alt"), q.Get("shift"), q.Get("meta"))
	w.Write([]byte("OK"))
}

func main() {
	pinFlag := flag.String("pin", "", "PIN code for authentication (defaults to HYPRCAST_PIN / CAST_PIN env var or random 6-digit PIN)")
	flag.Parse()

	castPIN = *pinFlag
	if castPIN == "" {
		castPIN = os.Getenv("HYPRCAST_PIN")
	}
	if castPIN == "" {
		castPIN = os.Getenv("CAST_PIN")
	}
	if castPIN == "" {
		castPIN = generateRandomPIN()
	}

	log.Println("==========================================")
	log.Printf("Hypr-Cast PIN: %s", castPIN)
	log.Println("==========================================")
	notifyPIN(castPIN)

	go captureLoop()

	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/logout", logoutHandler)
	http.HandleFunc("/stream", requireAuth(streamHandler))
	http.HandleFunc("/resize", requireAuth(resizeHandler))
	http.HandleFunc("/touch", requireAuth(touchHandler))
	http.HandleFunc("/key", requireAuth(keyHandler))
	http.HandleFunc("/debug", requireAuth(debugHandler))
	http.HandleFunc("/", indexHandler)

	log.Println("Persistent sRGB 4:4:4 libjpeg-turbo Go Cast Server listening on :8089...")
	log.Fatal(http.ListenAndServe("0.0.0.0:8089", nil))
}
