package handlers

import (
    "net/http"

    "github.com/gin-gonic/gin"
)

// Admin2Handler serves the new admin interface
func Admin2Handler(c *gin.Context) {
    c.HTML(http.StatusOK, "admin2.html", nil)
}

// Helper2Handler serves the new helper interface
func Helper2Handler(c *gin.Context) {
    c.HTML(http.StatusOK, "helper2.html", nil)
}