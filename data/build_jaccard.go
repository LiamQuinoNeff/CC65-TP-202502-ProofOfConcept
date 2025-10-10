// aca se toma el dataset limpio y construye
// una vista binaria por (usuario, juego) para usar el algoritmo jaccard.
// Regla de binarización (en orden de prioridad):
//   1) Si existe la columna `recommended`, se mapea a 1/0 (true/1/yes → 1; else → 0).
//   2) Si no hay `recommended` (o está vacío), se usa `author.playtime_at_review` (o la que indiques)
//      con un umbral `-hours_thresh` (por defecto 60). Si horas >= umbral → 1; si no → 0.
// Se escribe `clean_jaccard.csv` con columnas: user_id, item_id, value_binary.

package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	in := flag.String("in", "steam_reviews_clean.csv", "CSV limpio de entrada")
	out := flag.String("out", "clean_jaccard.csv", "CSV de salida para Jaccard")
	uCol := flag.String("user_col", "author.steamid", "Columna usuario")
	iCol := flag.String("item_col", "app_id", "Columna item")
	binCol := flag.String("bin_col", "recommended", "Columna binaria (recommended) si existe")
	hoursCol := flag.String("hours_col", "author.playtime_at_review", "Columna horas (fallback si no hay bin_col)")
	thresh := flag.Float64("hours_thresh", 60, "Umbral de horas para binarizar (>= → 1)")
	aggregate := flag.Bool("aggregate", true, "Si true, agrega múltiples filas (usuario,item) por OR")
	flag.Parse()

	f, err := os.Open(*in)
	check(err)
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))

	hdr, err := r.Read()
	check(err)
	iu, ii := idx(hdr, *uCol), idx(hdr, *iCol)
	ib, ih := idx(hdr, *binCol), idx(hdr, *hoursCol)
	if iu < 0 || ii < 0 {
		panic("No se encontraron columnas user/item")
	}
	if ib < 0 && ih < 0 {
		panic("No hay ni bin_col ni hours_col para binarizar")
	}

	// Si agregamos por OR, se acumulara en un map,  si no, escribiremos fila a fila.
	var outF *os.File
	var w *csv.Writer
	if !*aggregate {
		outF, err = os.Create(*out)
		check(err)
		defer outF.Close()
		w = csv.NewWriter(outF)
		_ = w.Write([]string{"user_id", "item_id", "value_binary"})
	}

	type key struct{ u, it string }
	agg := make(map[key]float64)

	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		u, it := rec[iu], rec[ii]
		if strings.TrimSpace(u) == "" || strings.TrimSpace(it) == "" {
			continue
		}

		val := 0.0
		if ib >= 0 && ib < len(rec) && strings.TrimSpace(rec[ib]) != "" {
			if toBool(rec[ib]) {
				val = 1.0
			}
		} else if ih >= 0 && ih < len(rec) { // Fallback: horas >= umbral
			if h, ok := toF(rec[ih]); ok && h >= *thresh {
				val = 1.0
			}
		}

		if *aggregate {
			k := key{u, it}
			if val > agg[k] { // OR
				agg[k] = val
			}
		} else {
			_ = w.Write([]string{u, it, fmtFloat(val)})
		}
	}

	if *aggregate {
		outF, err := os.Create(*out)
		check(err)
		defer outF.Close()
		w = csv.NewWriter(outF)
		_ = w.Write([]string{"user_id", "item_id", "value_binary"})
		for k, v := range agg {
			_ = w.Write([]string{k.u, k.it, fmtFloat(v)})
		}
		w.Flush()
		check(w.Error())
	} else {
		w.Flush()
		check(w.Error())
	}

	fmt.Println("OK →", *out)
}

func idx(h []string, name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, s := range h {
		if strings.ToLower(strings.TrimSpace(s)) == name {
			return i
		}
	}
	return -1
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

func toF(s string) (float64, bool) {
	x, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
	return x, err == nil
}

func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}
