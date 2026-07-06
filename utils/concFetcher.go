package utils

import (
	"context"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	log "github.com/sirupsen/logrus"
)

// Совершает конкурнетный запрос к HarborApi
// Оптимальное numOfWorkers ~количество REST запросов. Чтобы получить количество REST запросов,
// поделите количество сущностей, которые вы хотите получить на PageSize запроса
// Примерное количество сущностей харбора:
// Проекты ~ 100
// Репозитории ~ 3000
// Артефакты ~ 65000
func FetchConcurrently[T any](ctx context.Context, numOfWorkers int, fn func(context.Context, int64) (T, error), isDone func(T) bool, timeout time.Duration) ([]T, []error) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Инициализируем функцию генерирования чисел
	// В данном случае будут генерироваться числа 1,2,3,...
	pageCounter := pageNumCounter()

	// Запуск генератора чисел (номеров страниц)
	pageStream := gen(ctxWithTimeout, numOfWorkers, pageCounter)

	// Создаём массивы каналов, где будут лежать каналы всех воркеров
	resultsChans := make([]<-chan T, numOfWorkers)
	errorsChans := make([]<-chan error, numOfWorkers)

	// Запускаем воркеров, сохраняя возвращаемые ими каналы в массив
	for i := 0; i < numOfWorkers; i++ {
		resultsChans[i], errorsChans[i] = worker(ctxWithTimeout, pageStream, fn, isDone)
	}

	mergedResultsChan := aggregatorRes(ctxWithTimeout, resultsChans...)
	mergedErrorsChan := aggregatorErr(ctxWithTimeout, errorsChans...)

	mergedResultsArray, mergedErrorsArray := collector(ctxWithTimeout, mergedResultsChan, mergedErrorsChan)

	return mergedResultsArray, mergedErrorsArray
}

// Позволяет создать функцию-счётик, используя замыкание над переменной pageNum
// (последующие вызовы функции оперируют над одной и той же переменной pageNum)
// Используется для обхода пагинации при REST запросах
func pageNumCounter() func() int64 {
	var pageNum int64 = 0
	return func() int64 {
		pageNum++
		return pageNum
	}
}

// Функция возвращающая канал, в который происходит генерация чисел.
// Генерация чисел останавливается, когда вся работа по FetchConcurrently выполнена
func gen(ctxWithTimeout context.Context, numOfWorkers int, fn func() int64) <-chan int64 {
	// Делаем буфер в канале на размер количества воркеров
	stream := make(chan int64, numOfWorkers)
	// Горутина в которой идёт генерация чисел
	go func() {
		defer close(stream)
		for {
			select {
			case <-ctxWithTimeout.Done():
				return
			case stream <- fn():
			}
		}
	}()

	return stream
}

// Функция, выполняющая основную работу, возвращает каналы с ответами и ошибками
// Воркеров запускается несколько (в идеале по одному на количество REST запроса в API)
// На вход передаётся контекст для отмены по таймауту (Если запрос обрабатывается слишком долго)
// Канал с числами, которые будут использоваться для подстановки в номер страницы для REST запроса,
// (Чтобы получить все данные при ограниченом размере PageSize, который 100 у Harbor)
// Функция, которая и является REST запросом к API, сделано для того, чтобы можно было использовать
// Для разных запросов (получение проектов, репозиториев, артефактов)
// Функция, которая определит закончили ли мы работу, чтобы терминировать fetch
// Возвращает канал ответов и ошибок, которые закрываются, если:
// 1.Во время запроса мы встретили ошибку
// 2.Получили нулевой Payload, т.е. уже получили все данные
// На запросы настроены ретраи, с рандомной задержкой ( max = 3.5 секунд )
func worker[T any](ctxWithTimeout context.Context, stream <-chan int64, fn func(context.Context, int64) (T, error), isDone func(T) bool) (<-chan T, <-chan error) {
	results := make(chan T)
	errors := make(chan error)

	go func() {
		defer func() {
			close(results)
			close(errors)
			log.Trace("Worker is done fetching.")
			recoverFromPanic()
		}()
		for {
			select {
			case <-ctxWithTimeout.Done():
				return
			case pageNum := <-stream:
				response, err := retry.DoWithData(
					func() (T, error) {
						return fn(ctxWithTimeout, pageNum)
					},
					retry.Attempts(5),
					retry.DelayType(retry.RandomDelay),
					retry.MaxJitter(3500*time.Millisecond),
					retry.OnRetry(func(n uint, err error) {
						log.Debugf("Retrying request after error: %v", err)
					}),
				)
				if err != nil {
					errors <- err
					return
				} else if isDone(response) {
					return
				}
				log.Tracef("Worker successfully fetched response...")
				results <- response
			}
		}
	}()
	return results, errors
}

// Function that will allow to recover from and continue executing
func recoverFromPanic() {
	if r := recover(); r != nil {
		log.Errorf("Recovered from panic with: %v", r)
	}
}

// Функция, собирающая ответы со всех каналов воркеров и соединяющая их в один канал ответов
func aggregatorRes[T any](ctxWithTimeout context.Context, workerResultsChans ...<-chan T) <-chan T {
	// Waitgroup для того чтобы убедиться, что информация получена со всех каналов
	var wg sync.WaitGroup
	mergedResults := make(chan T)

	// Функция, которая будет применена к каждому каналу, который мы хотим смержить
	// Берёт данные из канала воркера и перекладывает данные в общий канал
	transfer := func(chanResults <-chan T) {
		defer wg.Done()
		// итерация по каналу происходит до тех пор, пока он не закрыт
		// закрывается он когда работа выполнена (т.к. это канал воркера)
		for result := range chanResults {
			select {
			case <-ctxWithTimeout.Done():
				return
			case mergedResults <- result:
			}
		}
	}

	// Для каждого канала воркера запускаем трансфер данных в общий канал
	for _, workerResultsChan := range workerResultsChans {
		wg.Add(1)
		go transfer(workerResultsChan)
	}

	// Ждём пока все данные не будут переданы, что происходит когда
	// все воркера завершили свою работу
	go func() {
		wg.Wait()
		close(mergedResults)
		log.Trace("Aggregator is done aggregating results from workers.")
	}()

	return mergedResults
}

// Функция, собирающая ошибки со всех каналов воркеров и соединяющая их в один канал ошибок.
// Делает всё точно также как и aggregatorRes, но только с ошибками
func aggregatorErr(ctxWithTimeout context.Context, workerErrChans ...<-chan error) <-chan error {
	var wg sync.WaitGroup
	mergedChanErrors := make(chan error)

	transfer := func(chanErrors <-chan error) {
		defer wg.Done()
		for err := range chanErrors {
			select {
			case <-ctxWithTimeout.Done():
				return
			case mergedChanErrors <- err:
			}
		}
	}

	for _, workerErrChan := range workerErrChans {
		wg.Add(1)
		go transfer(workerErrChan)
	}

	go func() {
		wg.Wait()
		close(mergedChanErrors)
		log.Trace("Aggregator is done aggregating errors from workers.")
	}()

	return mergedChanErrors
}

// Функция, собирающая информацию с каналов агрегаторов и помещающая ответы и ошибки в 2 различных массива.
// Запускаем 2 горутины на перекладывание результатов и ошибок с общих каналов в массивы
func collector[T any](ctxWithTimeout context.Context, resultChan <-chan T, errorsChan <-chan error) ([]T, []error) {
	var results []T
	var errors []error
	var wg sync.WaitGroup

	wg.Add(2)
	go func() []T {
		defer wg.Done()
		defer log.Trace("Collector is done collecting results from aggregatorRes.")
		for {
			select {
			case <-ctxWithTimeout.Done():
				return results
				// Когда канал закрыт, при чтении из него значения, которое попало туда
				// после закрытия
				// (после закрытия каналы начинают отдавать значение по дефолту для этого типа канала,
				// т.е. канал int будет отдавать 0, канал bool будет отдавать false и т.д. до бесконечности),
				// вторым параметром отдаётся false.
				// Если мы получили false, это значит что агрегатор закрыл общий канал, т.к. завершил
				// Агрегацию данных из каналов воркеров. Значит все данные мы уже обработали и можно
				// возвращать готовый массив
			case res, ok := <-resultChan:
				if !ok {
					return results
				}
				results = append(results, res)
			}
		}
	}()

	go func() []error {
		defer wg.Done()
		defer log.Trace("Collector is done collecting errors from aggregatorErr.")
		for {
			select {
			case <-ctxWithTimeout.Done():
				return errors
			case err, ok := <-errorsChan:
				if !ok {
					return errors
				}
				errors = append(errors, err)
			}
		}
	}()
	// Ждём пока все ошибки и все результаты не будут добавлены в результириющие массивы
	wg.Wait()
	return results, errors
}
