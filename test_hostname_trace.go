//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"html"
	"regexp"
)

func main() {
	hostName := "刑事辩护律师刘丽莎（全国办案）"
	fmt.Printf("原始: [%s]\n", hostName)
	fmt.Printf("长度: %d\n", len([]rune(hostName)))
	for i, r := range hostName {
		fmt.Printf("  [%d] %c (U+%04X)\n", i, r, r)
	}

	// Step 1: UnescapeHTMLEntity
	unescaped := html.UnescapeString(hostName)
	fmt.Printf("\nUnescapeHTMLEntity: [%s]\n", unescaped)

	// Step 2: ReplaceIllegalChar
	reg := regexp.MustCompile(`[\/\\\:\*\?\"\<\>\|]|[\.\s]+$`)
	illegalFiltered := unescaped
	for reg.MatchString(illegalFiltered) {
		illegalFiltered = reg.ReplaceAllString(illegalFiltered, "_")
	}
	fmt.Printf("ReplaceIllegalChar: [%s]\n", illegalFiltered)

	// Step 3: RemoveSymbolOtherChar
	reg2 := regexp.MustCompile(`\p{So}`)
	symbolFiltered := illegalFiltered
	for reg2.MatchString(symbolFiltered) {
		symbolFiltered = reg2.ReplaceAllString(symbolFiltered, "_")
	}
	fmt.Printf("RemoveSymbolOtherChar: [%s]\n", symbolFiltered)

	// Test with @ prefix
	fmt.Println("\n--- 测试 @ 前缀 ---")
	hostName2 := "@" + hostName
	fmt.Printf("原始带@: [%s]\n", hostName2)
	unescaped2 := html.UnescapeString(hostName2)
	fmt.Printf("UnescapeHTMLEntity: [%s]\n", unescaped2)
	illegalFiltered2 := unescaped2
	for reg.MatchString(illegalFiltered2) {
		illegalFiltered2 = reg.ReplaceAllString(illegalFiltered2, "_")
	}
	fmt.Printf("ReplaceIllegalChar: [%s]\n", illegalFiltered2)
	symbolFiltered2 := illegalFiltered2
	for reg2.MatchString(symbolFiltered2) {
		symbolFiltered2 = reg2.ReplaceAllString(symbolFiltered2, "_")
	}
	fmt.Printf("RemoveSymbolOtherChar: [%s]\n", symbolFiltered2)

	// Test with NickName = "@"
	fmt.Println("\n--- 测试 NickName = @ ---")
	nickName := "@"
	unescaped3 := html.UnescapeString(nickName)
	fmt.Printf("UnescapeHTMLEntity: [%s]\n", unescaped3)
	illegalFiltered3 := unescaped3
	for reg.MatchString(illegalFiltered3) {
		illegalFiltered3 = reg.ReplaceAllString(illegalFiltered3, "_")
	}
	fmt.Printf("ReplaceIllegalChar: [%s]\n", illegalFiltered3)
	symbolFiltered3 := illegalFiltered3
	for reg2.MatchString(symbolFiltered3) {
		symbolFiltered3 = reg2.ReplaceAllString(symbolFiltered3, "_")
	}
	fmt.Printf("RemoveSymbolOtherChar: [%s]\n", symbolFiltered3)

	// Test HTML entity
	fmt.Println("\n--- 测试 HTML 实体 ---")
	hostName3 := "&#64;刑事辩护律师刘丽莎（全国办案）"
	fmt.Printf("原始: [%s]\n", hostName3)
	unescaped4 := html.UnescapeString(hostName3)
	fmt.Printf("UnescapeHTMLEntity: [%s]\n", unescaped4)
}