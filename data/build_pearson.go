// vista numérica centrada POR USUARIO para el algoritmo de Pearson.
// Se usan tres columnas:
//   `author.steamid`  → identificador del usuario
//   `app_id`          → identificador del juego
//   <value_col>       → métrica continua (por defecto `author.playtime_at_review`)
// Salida: `clean_pearson.csv` con columnas: user_id, item_id, value_centered,
// donde value_centered = (value - mean_usuario). (Solo se emiten filas con value parseable.)

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
	out := flag.String("out", "clean_pearson.csv", "CSV de salida para Pearson")
	uCol := flag.String("user_col", "author.steamid", "Columna usuario")
	iCol := flag.String("item_col", "app_id", "Columna item")
	valCol := flag.String("value_col", "author.playtime_at_review", "Columna numérica a centrar por usuario")
	flag.Parse()

	f, err := os.Open(*in)
	check(err)
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))

	hdr, err := r.Read()
	check(err)
	iu, ii, iv := idx(hdr, *uCol), idx(hdr, *iCol), idx(hdr, *valCol)
	if iu < 0 || ii < 0 || iv < 0 {
		panic("No se encontraron columnas requeridas (user/item/value_col)")
	}

	// 1) Pasada 1: sumar y contar por usuario para obtener medias
	type agg struct {
		sum float64
		n   int
	}
	byU := make(map[string]agg)

	var buf [][]string // almacenamos filas crudas necesarias
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		u, it, v := rec[iu], rec[ii], rec[iv]
		if strings.TrimSpace(u) == "" || strings.TrimSpace(it) == "" {
			continue
		}
		if x, ok := toF(v); ok {
			a := byU[u]
			a.sum += x
			a.n++
			byU[u] = a
			buf = append(buf, []string{u, it, v}) // guardamos solo válidas
		}
	}

	// si no hay datos válidos, salir
	if len(buf) == 0 {
		fmt.Println("No se encontraron valores numéricos válidos en", *valCol)
		return
	}

	// medias por usuario
	mean := make(map[string]float64, len(byU))
	for u, a := range byU {
		if a.n > 0 {
			mean[u] = a.sum / float64(a.n)
		}
	}

	// 2) Pasada 2: escribir valor centrado (x - mean[u])
	outF, err := os.Create(*out)
	check(err)
	defer outF.Close()
	w := csv.NewWriter(outF)
	_ = w.Write([]string{"user_id", "item_id", "value_centered"})

	for _, rec := range buf {
		u, it, vs := rec[0], rec[1], rec[2]
		x, _ := toF(vs)
		c := x - mean[u]
		_ = w.Write([]string{u, it, fmtFloat(c)})
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

func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}
