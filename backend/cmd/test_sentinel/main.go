// cmd/test_sentinel/main.go
package main

import (
    "fmt"
    "log"
    "os"

    "github.com/joho/godotenv"
    "github.com/karvin-nanda/watchtower/internal/sentinel"
)

func main() {
    if err := godotenv.Load(".env"); err != nil {
        log.Fatal("gagal load .env:", err)
    }

    githubToken := os.Getenv("GITHUB_TOKEN") // opsional
    fetcher := sentinel.NewSentinelFetcher(githubToken)

    items, err := fetcher.FetchAll()
    if err != nil {
        log.Fatalf("[ERROR] FetchAll: %v", err)
    }

    fmt.Printf("Total items fetched: %d\n", len(items))

    // breakdown per source
    sourceCounts := make(map[string]int)
    for _, item := range items {
        sourceCounts[item.SourceType]++
    }

    for source, count := range sourceCounts {
        fmt.Printf("  %s: %d items\n", source, count)
    }

    // print 3 item pertama untuk sanity check
    fmt.Println("\nSample items:")
    for i, item := range items {
        if i >= 3 {
            break
        }
        fmt.Printf("\n[%d] Source: %s\n", i+1, item.SourceType)
        fmt.Printf("    ID: %s\n", item.Identifier)
        fmt.Printf("    Title: %s\n", item.Title)
        fmt.Printf("    Keywords: %v\n", item.Keywords)
    }
}