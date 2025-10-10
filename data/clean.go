package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	in := flag.String("in", "steam_reviews.csv", "Ruta al CSV")
	sep := flag.String("sep", ",", "Separador (por defecto ,)")
	save := flag.String("save", "", "Guardar CSV limpio (opcional)")
	colReview := flag.String("col_review", "review", "Columna texto a imputar con 'Desconocido'")
	colPlayAt := flag.String("col_playtime_at_review", "author.playtime_at_review", "Columna numérica a imputar con la mediana")
	flag.Parse()

	f, err := os.Open(*in)
	fatalIf(err)
	defer f.Close()

	r := csv.NewReader(bufio.NewReader(f))
	if *sep != "," && len(*sep) > 0 {
		r.Comma = rune((*sep)[0])
	}

	// Header
	header, err := r.Read()
	fatalIf(err)
	fmt.Println("Columnas:", header)

	// Leer filas
	var rows [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		// normaliza largo
		if len(rec) < len(header) {
			rec = append(rec, make([]string, len(header)-len(rec))...)
		} else if len(rec) > len(header) {
			rec = rec[:len(header)]
		}
		rows = append(rows, rec)
	}

	// Ejemplo
	if len(rows) > 0 {
		fmt.Println("\nEjemplo de fila (original):")
		for i, v := range rows[0] {
			fmt.Printf("%s: %s\n", header[i], v)
		}
	}

	// ---- INFO PRE (antes de imputar) ----
	fmt.Println("\n=== info simple (ANTES) ===")
	infoSimple(header, rows)

	// Localizar columnas
	jReview := indexOf(header, *colReview)
	jPlayAt := indexOf(header, *colPlayAt)
	if jReview < 0 {
		fmt.Printf("Aviso: no se encontró columna '%s' (no se imputará texto)\n", *colReview)
	}
	if jPlayAt < 0 {
		fmt.Printf("Aviso: no se encontró columna '%s' (no se imputará mediana)\n", *colPlayAt)
	}

	// Mediana de playtime_at_review (sobre valores numéricos válidos)
	median := 0.0
	if jPlayAt >= 0 {
		var vals []float64
		for _, rec := range rows {
			if v, ok := toFloat(rec[jPlayAt]); ok {
				vals = append(vals, v)
			}
		}
		if len(vals) > 0 {
			sort.Float64s(vals)
			median = quantile(vals, 0.5)
		}
	}

	// Imputar
	var cReview, cPlay int
	for i := range rows {
		if jReview >= 0 && strings.TrimSpace(rows[i][jReview]) == "" {
			rows[i][jReview] = "Desconocido"
			cReview++
		}
		if jPlayAt >= 0 {
			if _, ok := toFloat(rows[i][jPlayAt]); !ok || strings.TrimSpace(rows[i][jPlayAt]) == "" {
				rows[i][jPlayAt] = fmtFloat(median)
				cPlay++
			}
		}
	}

	// Ejemplo post
	if len(rows) > 0 {
		fmt.Println("\nEjemplo de fila (POST-imputación):")
		for i, v := range rows[0] {
			fmt.Printf("%s: %s\n", header[i], v)
		}
	}

	// ---- INFO POST (después de imputar) ----
	fmt.Println("\n=== info simple (DESPUÉS) ===")
	infoSimple(header, rows)
	fmt.Printf("\nImputaciones aplicadas → %s: %d | %s: %d (mediana=%.4f)\n",
		*colReview, cReview, *colPlayAt, cPlay, median)

	// Duplicados
	dups := countDuplicates(rows)
	fmt.Printf("Filas duplicadas: %d\n", dups)

	// Guardar
	outPath := *save
	if outPath == "" {
		outPath = "steam_reviews_clean.csv" // valor por defecto
	}
	if err := writeCSV(outPath, header, rows, r.Comma); err != nil {
		fmt.Println("Error guardando:", err)
		return
	}
	fmt.Println("Guardado en:", outPath)

}

/* helpers */

func infoSimple(header []string, rows [][]string) {
	cols := len(header)
	nonNull := make([]int, cols)
	numVotes := make([]int, cols)
	for _, rec := range rows {
		for j := 0; j < cols; j++ {
			val := strings.TrimSpace(rec[j])
			if val != "" {
				nonNull[j]++
				if _, ok := toFloat(val); ok {
					numVotes[j]++
				}
			}
		}
	}
	total := len(rows)
	fmt.Printf("Filas totales: %d | Columnas: %d\n", total, cols)
	fmt.Println("Columna                Tipo     NoNulos   Nulos   Nulos(%)")
	for j, h := range header {
		typ := "object"
		if nonNull[j] > 0 && float64(numVotes[j]) >= 0.6*float64(nonNull[j]) {
			typ = "float"
		}
		nulls := total - nonNull[j]
		var p float64
		if total > 0 {
			p = 100 * float64(nulls) / float64(total)
		}
		fmt.Printf("%-22s %-7s %8d %8d %9.2f%%\n", h, typ, nonNull[j], nulls, p)
	}
}

func indexOf(arr []string, name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, s := range arr {
		if strings.ToLower(strings.TrimSpace(s)) == name {
			return i
		}
	}
	return -1
}

func toFloat(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
	return v, err == nil
}

func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	if q <= 0 {
		return xs[0]
	}
	if q >= 1 {
		return xs[len(xs)-1]
	}
	pos := (float64(len(xs)) - 1) * q
	l := int(pos)
	u := l + 1
	if u >= len(xs) {
		return xs[l]
	}
	frac := pos - float64(l)
	return xs[l]*(1-frac) + xs[u]*frac
}

func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}

func countDuplicates(rows [][]string) int {
	seen := make(map[string]bool)
	dups := 0
	for _, r := range rows {
		key := strings.Join(r, "\x1f")
		if seen[key] {
			dups++
		} else {
			seen[key] = true
		}
	}
	return dups
}

func writeCSV(path string, header []string, rows [][]string, comma rune) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	w := csv.NewWriter(out)
	w.Comma = comma
	if err := w.Write(header); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
