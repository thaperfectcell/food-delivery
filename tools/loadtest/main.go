// Нагрузочный тест API Gateway: измеряет пропускную способность и задержки
// (p50/p95/p99) для типовых сценариев. Используется для проверки
// нефункциональных требований к производительности.
//
//	go run ./tools/loadtest -base http://localhost:8080 -c 50 -d 20s -scenario catalog
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	pizzeria   = "10000000-0000-4000-8000-000000000001"
	margherita = "11000000-0000-4000-8000-000000000001"
)

func main() {
	base := flag.String("base", "http://localhost:8080", "адрес шлюза")
	conc := flag.Int("c", 20, "число параллельных клиентов")
	dur := flag.Duration("d", 15*time.Second, "длительность теста")
	scenario := flag.String("scenario", "catalog", "catalog | menu | order | saga")
	flag.Parse()

	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 1000}}
	token := ""
	if *scenario == "order" || *scenario == "saga" {
		token = register(client, *base)
	}

	var (
		mu        sync.Mutex
		latencies []time.Duration
		errors    atomic.Int64
		statuses  sync.Map
	)
	deadline := time.Now().Add(*dur)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local []time.Duration
			for time.Now().Before(deadline) {
				t0 := time.Now()
				code, err := run(client, *base, *scenario, token)
				el := time.Since(t0)
				if err != nil {
					errors.Add(1)
					continue
				}
				v, _ := statuses.LoadOrStore(code, new(atomic.Int64))
				v.(*atomic.Int64).Add(1)
				if code < 300 {
					local = append(local, el)
				} else {
					errors.Add(1)
				}
			}
			mu.Lock()
			latencies = append(latencies, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(p float64) float64 {
		if len(latencies) == 0 {
			return 0
		}
		return float64(latencies[int(float64(len(latencies)-1)*p)].Microseconds()) / 1000
	}
	codes := map[int]int64{}
	statuses.Range(func(k, v any) bool { codes[k.(int)] = v.(*atomic.Int64).Load(); return true })
	res := map[string]any{
		"scenario": *scenario, "concurrency": *conc, "duration_s": elapsed.Seconds(),
		"ok": len(latencies), "errors": errors.Load(), "rps": float64(len(latencies)) / elapsed.Seconds(),
		"p50_ms": pct(0.50), "p95_ms": pct(0.95), "p99_ms": pct(0.99), "max_ms": pct(1), "codes": codes,
	}
	_ = json.NewEncoder(os.Stdout).Encode(res)
}

func run(c *http.Client, base, scenario, token string) (int, error) {
	switch scenario {
	case "catalog":
		return get(c, base+"/api/v1/restaurants", "")
	case "menu":
		return get(c, base+"/api/v1/restaurants/"+pizzeria+"/menu", "")
	case "order":
		code, _, err := createOrder(c, base, token)
		return code, err
	case "saga":
		// Время от оформления заказа до перехода в статус «оплачен».
		code, id, err := createOrder(c, base, token)
		if err != nil || code >= 300 {
			return code, err
		}
		for i := 0; i < 200; i++ {
			var o struct{ Status string }
			req, _ := http.NewRequest("GET", base+"/api/v1/orders/"+id, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := c.Do(req)
			if err != nil {
				return 0, err
			}
			_ = json.NewDecoder(resp.Body).Decode(&o)
			resp.Body.Close()
			if o.Status == "paid" {
				return 200, nil
			}
			time.Sleep(20 * time.Millisecond)
		}
		return 0, fmt.Errorf("saga timeout")
	}
	return 0, fmt.Errorf("unknown scenario %q", scenario)
}

func get(c *http.Client, url, token string) (int, error) {
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	var sink bytes.Buffer
	_, _ = sink.ReadFrom(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

func createOrder(c *http.Client, base, token string) (int, string, error) {
	body, _ := json.Marshal(map[string]any{
		"restaurant_id":    pizzeria,
		"items":            []map[string]any{{"menu_item_id": margherita, "quantity": 1}},
		"delivery_address": "Москва, ул. Нагрузочная, 1",
	})
	req, _ := http.NewRequest("POST", base+"/api/v1/orders", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	var o struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&o)
	return resp.StatusCode, o.ID, nil
}

func register(c *http.Client, base string) string {
	body, _ := json.Marshal(map[string]string{
		"email": "load-" + uuid.NewString()[:8] + "@test.local", "password": "password123",
		"name": "Нагрузочный тест", "role": "customer",
	})
	resp, err := c.Post(base+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "register:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var r struct{ Token string }
	_ = json.NewDecoder(resp.Body).Decode(&r)
	return r.Token
}
