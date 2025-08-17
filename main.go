package main

import (
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type Config struct {
	SourceDir      string
	DestinationDir string
	SyncInterval   time.Duration
}

type SyncManager struct {
	config Config
}

func NewSyncManager(config Config) *SyncManager {
	return &SyncManager{
		config: config,
	}
}

func (sm *SyncManager) Start() error {
	if err := sm.validateDirectories(); err != nil {
		return fmt.Errorf("ошибка валидации директорий: %w", err)
	}

	log.Println("Запуск синхронизации")

	if err := sm.sync(); err != nil {
		log.Printf("ошибка первоначальной синхронизации: %v", err)
	}

	return sm.runPeriodicSync()
}

func (sm *SyncManager) runPeriodicSync() error {
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(sm.config.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := sm.sync(); err != nil {
				log.Printf("Ошибка синхронизации: %v", err)
			}
		case <-stopChan:
			log.Println("Завершение по сигналу")
			return nil
		}
	}
}

func (sm *SyncManager) validateDirectories() error {
	if _, err := os.Stat(sm.config.SourceDir); os.IsNotExist(err) {
		return fmt.Errorf("исходная директория не существует: %s", sm.config.SourceDir)
	}

	if err := os.MkdirAll(sm.config.DestinationDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать целевую директорию: %w", err)
	}

	return nil
}

func (sm *SyncManager) sync() error {
	sourceFiles, err := sm.scanDirectory(sm.config.SourceDir)
	if err != nil {
		return fmt.Errorf("ошибка сканирования исходной директории: %w", err)
	}

	destFiles, err := sm.scanDirectory(sm.config.DestinationDir)
	if err != nil {
		return fmt.Errorf("ошибка сканирования целевой директории: %w", err)
	}

	copiedCount := 0
	for sourcePath, sourceInfo := range sourceFiles {
		destPath := sm.getDestinationPath(sourcePath)

		if sm.shouldCopyFile(sourcePath, sourceInfo, destFiles[sourcePath]) {
			if err := sm.copyFile(sourcePath, destPath); err != nil {
				log.Printf("Ошибка копирования файла %s: %v", sourcePath, err)
				continue
			}
			copiedCount++
			log.Printf("Скопирован файл: %s -> %s", sourcePath, destPath)
		}
	}

	deletedCount := 0
	for destPath := range destFiles {
		if _, exists := sourceFiles[destPath]; !exists {
			fullDestPath := filepath.Join(sm.config.DestinationDir, destPath)
			if err := sm.deleteFile(fullDestPath); err != nil {
				log.Printf("Ошибка удаления файла %s: %v", destPath, err)
				continue
			}
			deletedCount++
			log.Printf("Удален файл: %s", destPath)
		}
	}

	log.Printf("Синхронизация завершена. Скопировано: %d, удалено: %d", copiedCount, deletedCount)
	return nil
}

func (sm *SyncManager) scanDirectory(dir string) (map[string]os.FileInfo, error) {
	files := make(map[string]os.FileInfo)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if path == dir {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		relPath = filepath.ToSlash(relPath)
		files[relPath] = info

		return nil
	})

	return files, err
}

func (sm *SyncManager) getFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (sm *SyncManager) shouldCopyFile(sourcePath string, sourceInfo, destInfo os.FileInfo) bool {
	if destInfo == nil {
		return true
	}

	if sourceInfo.Size() != destInfo.Size() {
		return true
	}

	sourceHash, err := sm.getFileHash(sm.getSourcePath(sourcePath))
	if err != nil {
		log.Printf("Ошибка вычисления хеша исходного файла %s: %v", sourcePath, err)
		return true
	}

	destHash, err := sm.getFileHash(sm.getDestinationPath(sourcePath))
	if err != nil {
		log.Printf("Ошибка вычисления хеша целевого файла %s: %v", sourcePath, err)
		return true
	}

	if sourceHash != destHash {
		return true
	}

	return false
}

func (sm *SyncManager) copyFile(sourcePath, destPath string) error {
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию %s: %w", destDir, err)
	}

	source, err := os.Open(sm.getSourcePath(sourcePath))
	if err != nil {
		return fmt.Errorf("не удалось открыть исходный файл: %w", err)
	}
	defer source.Close()

	dest, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("не удалось создать целевой файл: %w", err)
	}
	defer dest.Close()

	if _, err := io.Copy(dest, source); err != nil {
		return fmt.Errorf("ошибка копирования содержимого: %w", err)
	}

	if err := dest.Sync(); err != nil {
		return fmt.Errorf("ошибка синхронизации файла: %w", err)
	}

	return nil
}

func (sm *SyncManager) deleteFile(filePath string) error {
	return os.Remove(filePath)
}

func (sm *SyncManager) getDestinationPath(sourcePath string) string {
	return filepath.Join(sm.config.DestinationDir, sourcePath)
}

func (sm *SyncManager) getSourcePath(sourcePath string) string {
	return filepath.Join(sm.config.SourceDir, sourcePath)
}

func main() {
	config := Config{
		SourceDir:      "source",
		DestinationDir: "destination",
		SyncInterval:   10 * time.Second,
	}

	syncManager := NewSyncManager(config)

	if err := syncManager.Start(); err != nil {
		log.Fatalf("Ошибка при запуске синхронизации: %v", err)
	}
}
