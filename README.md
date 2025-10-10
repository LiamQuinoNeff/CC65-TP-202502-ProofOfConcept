# PC2-PROGRAMACIÓN-CONCURRENTE Y DISTRIBUIDA
Este repositorio contiene implementaciones de tres métricas de similitud entre usuarios (Cosine, Jaccard y Pearson) en versiones secuenciales y concurrentes. Se añadió soporte para ejecutar los programas sobre cortes del dataset (porcentaje de filas) para comparar tiempos y encontrar puntos de equilibrio entre las versiones secuencial y concurrente.
## Requisitos
- Go 1.18+ instalado y disponible en PATH
- Dataset original: `steam_reviews.csv` (descargar desde Kaggle: https://www.kaggle.com/datasets/najzeko/steam-reviews-2021/data)
## Flujo básico de uso
1. Coloca `steam_reviews.csv` en la carpeta `data/` o pásalo con `--in` a los scripts de limpieza.
2. Ejecuta el script de limpieza para generar los CSV preparados:

	- Desde `data/`:
	```powershell
	Set-Location 'c:\path\to\repo\data'
	go run clean.go --in steam_reviews.csv --save steam_reviews_clean.csv
	go run build_cosine.go --in steam_reviews_clean.csv --out clean_cosine.csv
	go run build_pearson.go --in steam_reviews_clean.csv --out clean_pearson.csv
	go run build_jaccard.go --in steam_reviews_clean.csv --out clean_jaccard.csv
	```

3. Ir a la carpeta del algoritmo que quieras (por ejemplo `cosine`) y ejecutar los programas `seq.go` y `conc.go` con los flags adecuados (ver sección "Flags útiles").
## Flags útiles
- `--pct` (float): porcentaje de filas del CSV de entrada a usar (10, 25, 50, 100). Por defecto 100.
- `--reps` (int): número de repeticiones para medir tiempo. Recomendado 5 para medidas estables.
- `--workers` (int): número de workers para la versión concurrente (fijar para reproducibilidad, e.g. 8).
- `--out` (string): ruta de salida para guardar un CSV resumen con estadísticas.
- `--target` (string): user_id target. Si no existe en el subset, algunos programas (Pearson/Jaccard) pueden elegir un fallback automático.
- `--raw` (string): ruta al CSV limpio original para imprimir información humana sobre el target.

## Cómo ejecutar cortes y comparar
Ejemplo (PowerShell) dentro de `cosine/`:

```powershell
go run seq.go --pct=10 --reps=3 --out '..\results\cosine_seq_pct10.csv'
go run conc.go --pct=10 --workers=8 --reps=3 --out '..\results\cosine_conc_pct10.csv'
```

Repite para `pct=25,50,100`. Para `jaccard` y `pearson` los comandos son idénticos, cambiando la carpeta.
## Notas experimentales
- Usamos 3 repeticiones por defecto para balancear tiempo y estabilidad estadística. Para resultados finales se recomienda aumentar a 10.
- En Pearson se requiere al menos 2 ítems en común para calcular correlación válida; si el target queda fuera del subset el programa puede seleccionar un target alternativo (fallback).

## Recolección y análisis automático
- Cada programa puede generar un CSV resumen con `--out` que contiene columnas como `elapsed_ms_mean` y `elapsed_ms_std`. Estos CSVs permiten calcular speedup y eficiencia de forma automática.

## Soporte
Si quieres que unifique la interfaz (por ejemplo hacer que `--target` esté en todos los algoritmos y añadir muestreo aleatorio) dímelo y lo agrego.