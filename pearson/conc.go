package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Sim struct {
	User string
	Val  float64
}

func main() {
	input := flag.String("input", "../data/clean_pearson.csv", "CSV (user_id,item_id,value_centered)")
	raw := flag.String("raw", "../data/steam_reviews_clean.csv", "CSV limpio original para info humana")
	target := flag.String("target", "76561199095369542", "user_id target (por defecto embebido)")
	pct := flag.Float64("pct", 100.0, "Porcentaje (0-100) de filas a usar del CSV de entrada")
	showInfo := flag.Bool("showinfo", true, "Mostrar info TARGET y TOP-1 desde el CSV original")
	k := flag.Int("k", 20, "Top-K vecinos")
	reps := flag.Int("reps", 5, "Repeticiones para medir tiempo")
	workers := flag.Int("workers", 0, "Número de workers (0=GOMAXPROCS)")
	out := flag.String("out", "", "CSV resumen (opcional)")
	flag.Parse()

	if *workers <= 0 {
		*workers = runtime.GOMAXPROCS(0)
	}

	// ---- cargar clean_pearson.csv (posible corte con --pct) ----
	if *pct <= 0 {
		fmt.Println("pct debe ser > 0")
		os.Exit(1)
	}
	var limit int = -1
	if *pct < 100.0 {
		cf, err := os.Open(*input)
		check(err)
		cr := csv.NewReader(bufio.NewReader(cf))
		// header
		_, err = cr.Read()
		if err != nil {
			cf.Close()
			check(err)
		}
		var total int
		for {
			_, err := cr.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				continue
			}
			total++
		}
		cf.Close()
		if total == 0 {
			fmt.Println("No hay filas en el CSV de entrada")
			os.Exit(1)
		}
		limit = int(math.Ceil(float64(total) * (*pct) / 100.0))
		if limit < 1 {
			limit = 1
		}
	}

	f, err := os.Open(*input)
	check(err)
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))

	hdr, err := r.Read()
	check(err)
	iu := idx(hdr, "user_id")
	ii := idx(hdr, "item_id")
	iv := idx(hdr, "value_centered")
	if iu < 0 || ii < 0 || iv < 0 {
		panic("Se esperan columnas: user_id,item_id,value_centered")
	}

	// users[u][item] = value_centered
	users := make(map[string]map[string]float64)
	rows := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		u := rec[iu]
		it := rec[ii]
		x, ok := toF(rec[iv])
		if u == "" || it == "" || !ok {
			continue
		}
		if users[u] == nil {
			users[u] = map[string]float64{}
		}
		users[u][it] = x
		rows++
		if limit >= 0 && rows >= limit {
			break
		}
	}

	// validar target en el subset; si falta o tiene <2 items, seleccionar fallback
	targetID := *target
	if vec, ok := users[targetID]; !ok || len(vec) < 2 {
		var fallback string
		for u, v := range users {
			if len(v) >= 2 {
				fallback = u
				break
			}
		}
		if fallback == "" {
			fmt.Println("No se encontró ningún usuario con >=2 items en el subconjunto.")
			os.Exit(1)
		}
		if !ok {
			fmt.Printf("Aviso: target %s no existe en el subset. Usando fallback target=%s (items=%d)\n", targetID, fallback, len(users[fallback]))
		} else {
			fmt.Printf("Aviso: target %s tiene %d items en el subset (necesita >=2 para Pearson). Usando fallback target=%s (items=%d)\n", targetID, len(vec), fallback, len(users[fallback]))
		}
		targetID = fallback
	}

	// lista de usuarios (sin el target)
	userList := make([]string, 0, len(users)-1)
	for u := range users {
		if u != targetID {
			userList = append(userList, u)
		}
	}

	// ---- benchmark concurrente (solo correlaciones) ----
	var lastTop []Sim
	var lastCoverage int
	var lastMean, lastMax float64
	durations := make([]time.Duration, 0, *reps)

	for rep := 0; rep < *reps; rep++ {
		t0 := time.Now()
		top, coverage, meanVal, maxVal := pearsonConcurrent(users, targetID, userList, *workers, *k)
		durations = append(durations, time.Since(t0))
		lastTop, lastCoverage, lastMean, lastMax = top, coverage, meanVal, maxVal
	}

	tMean, tStd := meanStdMs(durations)

	// ---- salida ----
	fmt.Printf("Usuarios: %d | Filas: %d | Target=%s | K=%d | Workers=%d\n", len(users), rows, targetID, *k, *workers)
	fmt.Printf("Tiempo (concurrente) mean=%.2f ms  std=%.2f ms  reps=%d\n", tMean, tStd, *reps)
	fmt.Printf("Cobertura (corr válida): %d usuarios | corr_mean=%.4f | corr_max=%.4f\n", lastCoverage, lastMean, lastMax)
	fmt.Println("Top-K vecinos (user_id, pearson):")
	for i, s := range lastTop {
		fmt.Printf("%2d) %s\t%.6f\n", i+1, s.User, s.Val)
	}

	// CSV resumen opcional
	if *out != "" {
		saveSummary(*out, "pearson", "conc", *reps, rows, tMean, tStd, *k, lastCoverage, lastMean, lastMax)
	}

	// Info humana (igual que en seq)
	if *showInfo && len(lastTop) > 0 {
		top1 := lastTop[0].User
		fmt.Printf("\n=== INFO (CSV original) ===\n")
		showUserQuickInfo(*raw, targetID, "TARGET")
		showUserQuickInfo(*raw, top1, "TOP-1")
	}
}

// ---- concurrente eficiente: partición por bloques, sin canales por usuario ----
func pearsonConcurrent(users map[string]map[string]float64, target string, userList []string, workers, K int) ([]Sim, int, float64, float64) {
	// Particionar userList en N slices
	chunks := partition(userList, workers)

	type local struct {
		scores []Sim
		sum    float64
		cnt    int
		maxv   float64
	}

	var wg sync.WaitGroup
	part := make([]local, len(chunks))

	tv := users[target] // vector target

	wg.Add(len(chunks))
	for i := range chunks {
		go func(id int, slice []string) {
			defer wg.Done()
			loc := local{scores: make([]Sim, 0, len(slice))}
			for _, u := range slice {
				vec := users[u]
				// iterar por el mapa más pequeño para menos lookups
				var dot, ssx, ssy float64
				var common int
				if len(tv) <= len(vec) {
					for it, x := range tv {
						if y, ok := vec[it]; ok {
							dot += x * y
							ssx += x * x
							ssy += y * y
							common++
						}
					}
				} else {
					for it, y := range vec {
						if x, ok := tv[it]; ok {
							dot += x * y
							ssx += x * x
							ssy += y * y
							common++
						}
					}
				}
				if common < 2 {
					continue
				}
				den := math.Sqrt(ssx) * math.Sqrt(ssy)
				if den == 0 {
					continue
				}
				r := dot / den
				loc.scores = append(loc.scores, Sim{User: u, Val: r})
				loc.sum += r
				loc.cnt++
				if r > loc.maxv {
					loc.maxv = r
				}
			}
			part[id] = loc
		}(i, chunks[i])
	}
	wg.Wait()

	// merge resultados
	scores := make([]Sim, 0, len(userList))
	var sum float64
	var cnt int
	var maxv float64 = -1
	for _, p := range part {
		scores = append(scores, p.scores...)
		sum += p.sum
		cnt += p.cnt
		if p.maxv > maxv {
			maxv = p.maxv
		}
	}

	sort.Slice(scores, func(i, j int) bool { return scores[i].Val > scores[j].Val })
	if K > len(scores) {
		K = len(scores)
	}
	mean := 0.0
	if cnt > 0 {
		mean = sum / float64(cnt)
	}
	return scores[:K], cnt, mean, maxv
}

// ---- utilidades ----
func partition(ids []string, parts int) [][]string {
	if parts < 1 {
		parts = 1
	}
	n := len(ids)
	if n == 0 {
		return [][]string{{}}
	}
	if parts > n {
		parts = n
	}
	chunks := make([][]string, parts)
	step := (n + parts - 1) / parts // ceil(n/parts)
	for i := 0; i < n; i += step {
		j := i + step
		if j > n {
			j = n
		}
		chunks[i/step] = ids[i:j]
	}
	return chunks
}

func showUserQuickInfo(rawPath, userID, label string) {
	f, err := os.Open(rawPath)
	if err != nil {
		fmt.Printf("[%s] No se pudo abrir %s: %v\n", label, rawPath, err)
		return
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))

	hdr, err := r.Read()
	if err != nil {
		fmt.Printf("[%s] Error leyendo header: %v\n", label, err)
		return
	}
	iu := idx(hdr, "author.steamid")
	ia := idx(hdr, "app_id")
	irec := idx(hdr, "recommended")
	ipl := idx(hdr, "author.playtime_at_review")
	its := idx(hdr, "timestamp_created")
	if iu < 0 || ia < 0 || ipl < 0 || its < 0 {
		fmt.Printf("[%s] Faltan columnas esperadas en %s\n", label, rawPath)
		return
	}

	var n int
	var sumH float64
	var recYes int
	samples := make([][]string, 0, 3)

	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if rec[iu] != userID {
			continue
		}
		n++
		if v, ok := toF(rec[ipl]); ok {
			sumH += v
		}
		if irec >= 0 && toBool(rec[irec]) {
			recYes++
		}
		if len(samples) < 3 {
			recStr := "NA"
			if irec >= 0 {
				if toBool(rec[irec]) {
					recStr = "1"
				} else {
					recStr = "0"
				}
			}
			row := []string{rec[ia], recStr, rec[ipl], rec[its]}
			samples = append(samples, row)
		}
	}

	meanH := 0.0
	if n > 0 {
		meanH = sumH / float64(n)
	}
	pctRec := 0.0
	if n > 0 {
		pctRec = 100 * float64(recYes) / float64(n)
	}
	fmt.Printf("[%s] user_id=%s | reviews=%d | mean(playtime_at_review)=%.2f | %%recommended=%.2f%%\n", label, userID, n, meanH, pctRec)
	if len(samples) > 0 {
		fmt.Printf("[%s] muestras (app_id, recommended, playtime_at_review, timestamp_created):\n", label)
		for _, s := range samples {
			fmt.Printf("   %s, %s, %s, %s\n", s[0], s[1], s[2], s[3])
		}
	}
}

func toBool(s string) bool {
	ls := strings.ToLower(strings.TrimSpace(s))
	switch ls {
	case "1", "true", "t", "yes", "y", "si", "sí":
		return true
	case "0", "false", "f", "no", "n":
		return false
	default:
		if f, ok := toF(ls); ok {
			return f != 0
		}
		return false
	}
}

func idx(h []string, name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, v := range h {
		if strings.ToLower(strings.TrimSpace(v)) == name {
			return i
		}
	}
	return -1
}
func toF(s string) (float64, bool) {
	x, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
	return x, err == nil
}
func meanStdMs(ds []time.Duration) (mean, std float64) {
	if len(ds) == 0 {
		return 0, 0
	}
	var sum float64
	for _, d := range ds {
		sum += float64(d.Milliseconds())
	}
	mean = sum / float64(len(ds))
	if len(ds) == 1 {
		return mean, 0
	}
	var ss float64
	for _, d := range ds {
		diff := float64(d.Milliseconds()) - mean
		ss += diff * diff
	}
	std = math.Sqrt(ss / float64(len(ds)-1))
	return
}
func saveSummary(path, algo, mode string, reps, rows int, meanMs, stdMs float64, k, coverage int, corrMean, corrMax float64) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Println("no se pudo guardar resumen:", err)
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"algo", "mode", "reps", "rows", "elapsed_ms_mean", "elapsed_ms_std", "k", "coverage", "corr_mean", "corr_max"})
	_ = w.Write([]string{
		algo, mode,
		strconv.Itoa(reps),
		strconv.Itoa(rows),
		fmtFloat(meanMs),
		fmtFloat(stdMs),
		strconv.Itoa(k),
		strconv.Itoa(coverage),
		fmtFloat(corrMean),
		fmtFloat(corrMax),
	})
	w.Flush()
}
func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }
func check(e error) {
	if e != nil {
		panic(e)
	}
}
