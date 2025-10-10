// esto estara listo para usar el algoritmo Cosine con tres señales informativas por (usuario, juego):
//   engagement = log1p(playtime_at_review) normalizado POR USUARIO a [0,1]
//   preference = recommended en binario (1 si true/1/yes, 0 en caso contrario)
//   recency    = timestamp_created reescalado a [0,1] (más reciente = 1)
// La salida (clean_cosine.csv) incluye: user_id, item_id, engagement, preference, recency.

package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

type row struct {
	u, it, recRaw, playRaw, tsRaw string
}

func main() {
	in := flag.String("in", "steam_reviews_clean.csv", "CSV limpio de entrada")
	out := flag.String("out", "clean_cosine.csv", "CSV de salida para Cosine")
	uCol := flag.String("user_col", "author.steamid", "Columna usuario")
	iCol := flag.String("item_col", "app_id", "Columna item")
	recCol := flag.String("rec_col", "recommended", "Columna preferencia (recommended)")
	playCol := flag.String("play_col", "author.playtime_at_review", "Columna tiempo de juego a la reseña")
	tsCol := flag.String("ts_col", "timestamp_created", "Columna timestamp (epoch segundos)")
	flag.Parse()

	f, err := os.Open(*in)
	check(err)
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))

	hdr, err := r.Read()
	check(err)
	iu, ii, ir, ip, it := idx(hdr, *uCol), idx(hdr, *iCol), idx(hdr, *recCol), idx(hdr, *playCol), idx(hdr, *tsCol)
	if iu < 0 || ii < 0 || ir < 0 || ip < 0 || it < 0 {
		panic("No se encontraron columnas requeridas (user/item/recommended/play/ts)")
	}

	// Primera pasada: recolectar filas y stats para normalizar
	var rows []row
	byUmin := map[string]float64{}
	byUmax := map[string]float64{}
	minTS, maxTS := math.Inf(1), math.Inf(-1)

	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		u, item := rec[iu], rec[ii]
		recRaw, playRaw, tsRaw := rec[ir], rec[ip], rec[it]
		rows = append(rows, row{u, item, recRaw, playRaw, tsRaw})

		// engagement: acumulamos min/max por usuario sobre log1p(play)
		if p, ok := toF(playRaw); ok {
			lp := math.Log1p(p)
			if _, ok := byUmin[u]; !ok {
				byUmin[u], byUmax[u] = lp, lp
			} else {
				if lp < byUmin[u] {
					byUmin[u] = lp
				}
				if lp > byUmax[u] {
					byUmax[u] = lp
				}
			}
		}
		// recency: min/max global de timestamp
		if ts, ok := toF(tsRaw); ok {
			if ts < minTS {
				minTS = ts
			}
			if ts > maxTS {
				maxTS = ts
			}
		}
	}

	// Segunda pasada: escribir features normalizados
	outF, err := os.Create(*out)
	check(err)
	defer outF.Close()
	w := csv.NewWriter(outF)
	_ = w.Write([]string{"user_id", "item_id", "engagement", "preference", "recency"})

	for _, rw := range rows {
		// engagement
		eng := 0.0
		if p, ok := toF(rw.playRaw); ok {
			lp := math.Log1p(p)
			den := byUmax[rw.u] - byUmin[rw.u]
			if den > 0 {
				eng = (lp - byUmin[rw.u]) / den
			} else {
				eng = 0.0
			}
			eng = clamp01(eng)
		}

		// preference
		pref := 0.0
		if b01(rw.recRaw) {
			pref = 1.0
		}

		// recency
		rec := 0.0
		if ts, ok := toF(rw.tsRaw); ok && maxTS > minTS {
			rec = (ts - minTS) / (maxTS - minTS)
			rec = clamp01(rec)
		}

		_ = w.Write([]string{
			rw.u, rw.it,
			fmtFloat(eng), fmtFloat(pref), fmtFloat(rec),
		})
	}
	w.Flush()
	check(w.Error())
	fmt.Println("OK →", *out)
}

/* helpers */

func idx(h []string, name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, s := range h {
		if strings.ToLower(strings.TrimSpace(s)) == name {
			return i
		}
	}
	return -1
}
func toF(s string) (float64, bool) {
	x, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
	return x, err == nil
}
func b01(s string) bool {
	ls := strings.ToLower(strings.TrimSpace(s))
	return ls == "1" || ls == "true" || ls == "yes" || ls == "y"
}
func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}
func check(e error) {
	if e != nil {
		panic(e)
	}
}
