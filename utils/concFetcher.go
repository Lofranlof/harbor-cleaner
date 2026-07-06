package utils

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/avast/retry-go/v4"
	log "github.com/sirupsen/logrus"
)

// Совершает конкурнетный запрос к HarborApi
//
// Запросы страниц (1,2,3,...) разбираются numOfWorkers горутинами через общий
// атомарный счётчик. На каждый запрос настроены ретраи со случайной задержкой
// (max = 3.5 секунды). Воркеры шлют результаты и ошибки в общие каналы -
// единственный читающий цикл ниже раскладывает их по срезам, так что срезы
// трогает только одна горутина и мьютекс не нужен.
// Как только один воркер получает неустранимую ошибку, остальные воркеры
// останавливаются - продолжать пагинацию нет смысла, раз результат всё равно
// будет отброшен вызывающим кодом.
func FetchConcurrently[T any](ctx context.Context, numOfWorkers int, fn func(context.Context, int64) (T, error), isDone func(T) bool, timeout time.Duration) ([]T, []error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var pageNum int64
	resultsCh := make(chan T)
	errsCh := make(chan error)

	var wg sync.WaitGroup
	wg.Add(numOfWorkers)
	for i := 0; i < numOfWorkers; i++ {
		go func() {
			defer wg.Done()
			defer recoverFromPanic()
			for {
				select {
				case <-ctxWithTimeout.Done():
					return
				default:
				}

				page := atomic.AddInt64(&pageNum, 1)
				response, err := retry.DoWithData(
					func() (T, error) {
						return fn(ctxWithTimeout, page)
					},
					retry.Attempts(5),
					retry.DelayType(retry.RandomDelay),
					retry.MaxJitter(3500*time.Millisecond),
					retry.OnRetry(func(n uint, err error) {
						log.Debugf("Retrying request after error: %v", err)
					}),
				)
				if err != nil {
					select {
					case errsCh <- err:
					case <-ctxWithTimeout.Done():
					}
					cancel() // fail-fast: other workers stop instead of paginating uselessly
					return
				}
				if isDone(response) {
					return
				}
				log.Tracef("Worker successfully fetched response...")
				select {
				case resultsCh <- response:
				case <-ctxWithTimeout.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resultsCh)
		close(errsCh)
	}()

	var results []T
	var errs []error
	for resultsCh != nil || errsCh != nil {
		select {
		case r, ok := <-resultsCh:
			if !ok {
				resultsCh = nil
				continue
			}
			results = append(results, r)
		case e, ok := <-errsCh:
			if !ok {
				errsCh = nil
				continue
			}
			errs = append(errs, e)
		}
	}

	return results, errs
}

// Function that will allow to recover from and continue executing
func recoverFromPanic() {
	if r := recover(); r != nil {
		log.Errorf("Recovered from panic with: %v", r)
	}
}
