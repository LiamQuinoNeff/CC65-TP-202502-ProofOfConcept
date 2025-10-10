package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Sim struct {
	User string
	Val  float64
}

var target = "76561199095369542"

func main() {
	input := flag.String("input", "../data/clean_cosine.csv", "CSV (user_id,item_id,engagement,preference,recency)")
	pct := flag.Float64("pct", 100.0, "Porcentaje (0-100) de filas a usar del CSV de entrada")
	raw := flag.String("raw", "../data/steam_reviews_clean.csv", "CSV limpio original para mostrar info")
	showInfo := flag.Bool("showinfo", true, "Mostrar info TARGET y TOP-1 usando CSV original")
	workers := flag.Int("workers", 8, "Número de workers")
	k := flag.Int("k", 20, "Top-K vecinos")
	reps := flag.Int("reps", 5, "Repeticiones para medir tiempo")
	wEng := flag.Float64("w_eng", 0.5, "Peso engagement")
	wPref := flag.Float64("w_pref", 0.3, "Peso preference")
	wRec := flag.Float64("w_rec", 0.2, "Peso recency")
	out := flag.String("out", "", "CSV resumen (opcional)")
	flag.Parse()

	// ---- cargar clean_cosine.csv (posible corte con --pct) ----
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
	iu, ii := idx(hdr, "user_id"), idx(hdr, "item_id")
	ie, ip, ir := idx(hdr, "engagement"), idx(hdr, "preference"), idx(hdr, "recency")
	if iu < 0 || ii < 0 || ie < 0 || ip < 0 || ir < 0 {
		panic("Se esperan columnas: user_id,item_id,engagement,preference,recency")
	}

	// users[u][item] = weight
	users := make(map[string]map[string]float64)
	rows := 0
	// read rows; if limit>=0 stop after reaching limit valid rows
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
		eng, ok1 := toF(rec[ie])
		pref, ok2 := toF(rec[ip])
		recn, ok3 := toF(rec[ir])
		if u == "" || it == "" || !(ok1 && ok2 && ok3) {
			continue
		}
		w := (*wEng)*eng + (*wPref)*pref + (*wRec)*recn
		if users[u] == nil {
			users[u] = map[string]float64{}
		}
		users[u][it] = w
		rows++
		if limit >= 0 && rows >= limit {
			break
		}
	}

	if _, ok := users[target]; !ok {
		fmt.Printf("El user_id target %s no existe en %s\n", target, *input)
		os.Exit(1)
	}

	// precalcular ||u||
	norm := make(map[string]float64, len(users))
	for u, vec := range users {
		var s float64
		for _, w := range vec {
			s += w * w
		}
		norm[u] = math.Sqrt(s)
	}

	// preparar jobs: todos los usuarios excepto el target
	userList := make([]string, 0, len(users)-1)
	for u := range users {
		if u != target {
			userList = append(userList, u)
		}
	}

	// ---- benchmark concurrente ----
	var lastTop []Sim
	var lastCoverage int
	var lastMeanSim, lastMaxSim float64
	durations := make([]time.Duration, 0, *reps)

	for rep := 0; rep < *reps; rep++ {
		t0 := time.Now()
		top, coverage, meanSim, maxSim := cosineConcurrent(users, norm, target, userList, *workers, *k)
		durations = append(durations, time.Since(t0))
		lastTop, lastCoverage, lastMeanSim, lastMaxSim = top, coverage, meanSim, maxSim
	}

	tMean, tStd := meanStdMs(durations)

	// ---- salida ----
	fmt.Printf("Usuarios: %d | Filas: %d | Target=%s | K=%d | Workers=%d\n", len(users), rows, target, *k, *workers)
	fmt.Printf("Tiempo (concurrente) mean=%.2f ms  std=%.2f ms  reps=%d\n", tMean, tStd, *reps)
	fmt.Printf("Cobertura (cos>0): %d usuarios | sim_mean=%.4f | sim_max=%.4f\n", lastCoverage, lastMeanSim, lastMaxSim)
	fmt.Println("Top-K vecinos (user_id, cosine):")
	for i, s := range lastTop {
		fmt.Printf("%2d) %s\t%.6f\n", i+1, s.User, s.Val)
	}

	if *out != "" {
		saveSummary(*out, "cosine", "conc", *workers, *reps, rows, tMean, tStd, *k, lastCoverage, lastMeanSim, lastMaxSim)
	}

	// ---- info del target y TOP-1 (CSV original) ----
	if *showInfo && len(lastTop) > 0 {
		top1 := lastTop[0].User
		fmt.Printf("\n=== INFO (CSV original) ===\n")
		showUserQuickInfo(*raw, target, "TARGET")
		showUserQuickInfo(*raw, top1, "TOP-1")
	}
}

// ---- worker pool ----
func cosineConcurrent(users map[string]map[string]float64, norm map[string]float64, target string, userList []string, workers, K int) ([]Sim, int, float64, float64) {
	type job struct{ u string }
	type res struct {
		u   string
		val float64
	}

	jobs := make(chan job, 1024)
	results := make(chan res, 1024)

	// workers
	for w := 0; w < workers; w++ {
		go func() {
			tv := users[target]
			tn := norm[target]
			for j := range jobs {
				vec := users[j.u]
				// producto punto solo en items comunes
				var dot float64
				for it, w := range tv {
					if w2, ok := vec[it]; ok {
						dot += w * w2
					}
				}
				den := tn * norm[j.u]
				if den > 0 {
					cos := dot / den
					if cos > 0 {
						results <- res{u: j.u, val: cos}
					} else {
						results <- res{u: j.u, val: 0}
					}
				} else {
					results <- res{u: j.u, val: 0}
				}
			}
		}()
	}

	// feeder
	go func() {
		for _, u := range userList {
			jobs <- job{u: u}
		}
		close(jobs)
	}()

	// collect
	scores := make([]Sim, 0, len(userList))
	var sum float64
	var cnt int
	var maxv float64

	for i := 0; i < len(userList); i++ {
		r := <-results
		if r.val > 0 {
			scores = append(scores, Sim{User: r.u, Val: r.val})
			sum += r.val
			cnt++
			if r.val > maxv {
				maxv = r.val
			}
		}
	}
	close(results)

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

// ---------- INFO desde CSV original ----------
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

// ---------- helpers compartidos ----------
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
func readSeqMean(path string) (float64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))
	_, _ = r.Read() // header
	rec, err := r.Read()
	if err != nil || len(rec) < 6 {
		return 0, false
	}
	// columna "elapsed_ms_mean" es la 6 (index 5)
	val, err := strconv.ParseFloat(strings.TrimSpace(rec[5]), 64)
	if err != nil {
		return 0, false
	}
	return val, true
}
func saveSummary(path, algo, mode string, workers, reps, rows int, meanMs, stdMs float64, k, coverage int, simMean, simMax float64) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Println("no se pudo guardar summary:", err)
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"algo", "mode", "workers", "reps", "rows", "elapsed_ms_mean", "elapsed_ms_std", "k", "coverage", "sim_mean", "sim_max"})
	_ = w.Write([]string{
		algo, mode,
		strconv.Itoa(workers),
		strconv.Itoa(reps),
		strconv.Itoa(rows),
		fmtFloat(meanMs),
		fmtFloat(stdMs),
		strconv.Itoa(k),
		strconv.Itoa(coverage),
		fmtFloat(simMean),
		fmtFloat(simMax),
	})
	w.Flush()
}
func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }
func check(e error) {
	if e != nil {
		panic(e)
	}
}
