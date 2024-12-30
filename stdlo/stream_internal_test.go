package stdlo

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMap(t *testing.T) {
	t.Parallel()
	is := assert.New(t)

	result1 := Map([]int{1, 2, 3, 4}, func(x int, _ int) string {
		return "Hello"
	})
	result2 := Map([]int64{1, 2, 3, 4}, func(x int64, _ int) string {
		return strconv.FormatInt(x, 10)
	})

	is.Len(result1, 4)
	is.Len(result2, 4)
	is.Equal([]string{"Hello", "Hello", "Hello", "Hello"}, result1)
	is.Equal([]string{"1", "2", "3", "4"}, result2)
}
