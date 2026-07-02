package main

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/karvin-nanda/watchtower/internal/asset"
	"github.com/karvin-nanda/watchtower/internal/currency"
)

func main() {
	if err := godotenv.Load(".env"); err != nil {
		log.Fatal("gagal load .env:", err)
	}

	twelveKey := os.Getenv("TWELVE_DATA_API_KEY")
	if twelveKey == "" {
		log.Fatal("TWELVE_DATA_API_KEY kosong di .env")
	}
	fmt.Println("[DEBUG] API key loaded, length:",
		len(twelveKey))

	fetcher := asset.NewAssetFetcher(twelveKey)

	// Test currency
	rate, err := currency.GetUSDToIDR()
	if err != nil {
		log.Printf("[ERROR] currency: %v", err)
	} else {
		fmt.Printf("USD/IDR rate: %.2f\n", rate)
	}

	// Test crypto
	btc, err := fetcher.FetchAsset("BTC", "crypto")
	if err != nil {
		log.Printf("[ERROR] FetchCrypto BTC: %v", err)
	} else {
		fmt.Printf("BTC: $%.2f (%.2f%%)\n",
			btc.PriceUSD, btc.ChangePct24h)
	}

	// Test stock
	nvda, err := fetcher.FetchAsset("NVDA", "stock")
	if err != nil {
		log.Printf("[ERROR] FetchStock NVDA: %v", err)
	} else {
		fmt.Printf("NVDA: $%.2f (%.2f%%)\n",
			nvda.PriceUSD, nvda.ChangePct24h)
	}

	// Test gold
	gold, err := fetcher.FetchAsset("ANTAM", "gold")
	if err != nil {
		log.Printf("[ERROR] FetchGold: %v", err)
	} else {
		fmt.Printf("GOLD: $%.4f\n", gold.PriceUSD)
	}
}
